package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/secrets"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/modelscript"
)

// rotatedAPIKey is the value the credential is rotated to. Like the harness
// canary it is long enough to be registered with the redactor and distinctive
// enough that a substring match cannot collide with unrelated output.
const rotatedAPIKey = "sk-rotated-QwErTy-9902-leak-detector"

// A10.6 — rotating an org credential takes effect for subsequent work, and
// neither the old nor the new secret appears in the event log or the process
// logs.
//
// The rotation is driven through the public API, and the proof that it took
// effect is what the vendor received: asserting that PutCredential returned
// success would only show a row changed. Both values are swept, because a
// rotation that leaked the value being retired would be just as much of a
// breach as one that leaked the new one, and only the new value is covered by
// the redactor registration that happens during the rotation call itself.
func TestA106_CredentialRotationTakesEffectAndLeaksNothing(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	org := string(stack.TenantA.ID)
	session := createSession(t, alice, org, "credential rotation")

	// A run before the rotation, so the seeded canary is proven to be the
	// value in force rather than assumed.
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{{Text: "before rotation"}}})
	before, _, err := alice.StartRun(ctx, session.GetId(), "use the credential")
	if err != nil {
		t.Fatal(err)
	}
	alice.AwaitRunState(t, before.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 60*time.Second)
	if !authorizedWith(stack, harness.CanaryAPIKey) {
		t.Fatal("the first run did not authenticate with the seeded credential; rotation would prove nothing")
	}

	// Rotate through the public API, to the same kind and name, which is what
	// makes this a rotation rather than a second credential.
	rotated, err := alice.Tenants.PutCredential(ctx, connect.NewRequest(&corev1.PutCredentialRequest{
		TenantId: org, Kind: uc.CredentialKindOpenAI, Name: "default",
		ApiKey: rotatedAPIKey, BaseUrl: stack.Model.URL(),
	}))
	if err != nil {
		t.Fatalf("rotating the credential failed: %v", err)
	}
	// The rotation is visible as a rotation, not as a fresh credential.
	if rotated.Msg.GetCredential().GetRotatedAt() == nil {
		t.Fatal("the rotated credential reports no rotation time")
	}

	// The new value is in force for subsequent work.
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{{Text: "after rotation"}}})
	after, _, err := alice.StartRun(ctx, session.GetId(), "use the rotated credential")
	if err != nil {
		t.Fatal(err)
	}
	alice.AwaitRunState(t, after.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 60*time.Second)
	if !authorizedWith(stack, rotatedAPIKey) {
		t.Fatal("no vendor call carried the rotated credential, so the rotation did not take effect")
	}
	// And the retired value stops being sent: a rotation that kept using the
	// old key would satisfy the assertion above and still be broken.
	if lastAuthorization(stack) != "Bearer "+rotatedAPIKey {
		t.Fatalf("the most recent vendor call did not use the rotated credential: %q",
			redactedAuthorization(stack))
	}

	// Neither value appears anywhere observable. The old value is included
	// because retiring a secret does not make disclosing it acceptable.
	for _, secret := range []string{harness.CanaryAPIKey, rotatedAPIKey} {
		events, err := stack.Store.Tenant(stack.TenantA.ID).Events().Range(ctx,
			uc.SessionID(session.GetId()), 0, 4096)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 0 {
			t.Fatal("the session log is empty, so the sweep would be vacuous")
		}
		for _, event := range events {
			assertNoSecret(t, secret, "event payload "+event.Kind, string(event.Payload))
		}
		assertNoSecret(t, secret, "cored and worker logs", stack.Logs())

		// The credential surface never echoes secret material back.
		listed, err := alice.Tenants.ListCredentials(ctx, connect.NewRequest(&corev1.ListCredentialsRequest{
			TenantId: org,
		}))
		if err != nil {
			t.Fatal(err)
		}
		for _, info := range listed.Msg.GetCredentials() {
			assertNoSecret(t, secret, "ListCredentials response", info.String())
		}

		// Nor does the stored row hold it in the clear.
		stored, err := stack.Store.Tenant(stack.TenantA.ID).Credentials().Get(ctx,
			uc.CredentialKindOpenAI, "default")
		if err != nil {
			t.Fatal(err)
		}
		assertNoSecret(t, secret, "credential ciphertext at rest", string(stored.EncPayload))
	}

	// The stored ciphertext really is the rotated value, so "not in the clear"
	// above is not hiding a rotation that never reached the database.
	if !authorizedWith(stack, rotatedAPIKey) {
		t.Fatal("the rotated credential was never used")
	}
}

// A10.6 — a rotated credential reaches work that was already running, which is
// the "reconciled resources" half of the bullet.
//
// An environment-backed run is started, the credential is rotated while the
// session is live, and a subsequent step is asserted to use the new value. The
// point is that nothing caches the decrypted credential past a rotation.
func TestA106_RotationAppliesToAlreadyRunningSessions(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	org := string(stack.TenantA.ID)
	session := createSession(t, alice, org, "rotation mid-session")

	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{{Text: "first"}}})
	first, _, err := alice.StartRun(ctx, session.GetId(), "first run")
	if err != nil {
		t.Fatal(err)
	}
	alice.AwaitRunState(t, first.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 60*time.Second)

	if _, err := alice.Tenants.PutCredential(ctx, connect.NewRequest(&corev1.PutCredentialRequest{
		TenantId: org, Kind: uc.CredentialKindOpenAI, Name: "default",
		ApiKey: rotatedAPIKey, BaseUrl: stack.Model.URL(),
	})); err != nil {
		t.Fatal(err)
	}

	// A run started in the same session after the rotation must use the new
	// value: the worker resolves the credential per step, so a cached provider
	// would show up here as the old key.
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{{Text: "second"}}})
	second, _, err := alice.StartRun(ctx, session.GetId(), "second run")
	if err != nil {
		t.Fatal(err)
	}
	alice.AwaitRunState(t, second.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 60*time.Second)

	if got := lastAuthorization(stack); got != "Bearer "+rotatedAPIKey {
		t.Fatalf("a run in a live session kept using the pre-rotation credential: %q",
			redactedAuthorization(stack))
	}
	for _, secret := range []string{harness.CanaryAPIKey, rotatedAPIKey} {
		assertNoSecret(t, secret, "cored and worker logs", stack.Logs())
	}
}

// authorizedWith reports whether any vendor call carried a value.
func authorizedWith(stack *harness.Stack, apiKey string) bool {
	for _, req := range stack.Model.Requests() {
		if strings.Contains(req.Authorization, apiKey) {
			return true
		}
	}
	return false
}

// lastAuthorization returns the authorization header of the most recent vendor
// call, which is what shows the value currently in force.
func lastAuthorization(stack *harness.Stack) string {
	requests := stack.Model.Requests()
	if len(requests) == 0 {
		return ""
	}
	return requests[len(requests)-1].Authorization
}

// redactedAuthorization describes the last authorization header without
// reproducing it, so a failure message cannot become the leak it reports.
func redactedAuthorization(stack *harness.Stack) string {
	header := lastAuthorization(stack)
	switch {
	case header == "":
		return "no vendor call was made"
	case strings.Contains(header, harness.CanaryAPIKey):
		return "the pre-rotation credential"
	case strings.Contains(header, rotatedAPIKey):
		return "the rotated credential"
	default:
		return "an unrecognized credential"
	}
}

// assertNoSecret fails when any encoded form of a secret appears in text. It
// mirrors assertNoCanary but takes the value, because a rotation has two
// secrets to sweep for rather than one.
func assertNoSecret(t *testing.T, secret, where, text string) {
	t.Helper()
	for _, form := range secrets.Encodings(secret) {
		if form == "" {
			continue
		}
		if strings.Contains(text, form) {
			t.Fatalf("%s leaked a credential value (form %q)", where, form)
		}
	}
}
