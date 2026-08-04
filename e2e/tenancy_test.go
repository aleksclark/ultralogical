package e2e_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/testclient"
)

// TestA31_CrossTenantInvisibility covers A3.2: missing and cross-tenant are
// indistinguishable on sessions, runs, credentials, providers, labels, events.
func TestA31_CrossTenantInvisibility(t *testing.T) {
	stack := harness.Up(t)
	ctx := context.Background()
	alice := stack.AliceClient()
	bob := stack.BobClient()

	sess, err := alice.Sessions.CreateSession(ctx, connect.NewRequest(&corev1.CreateSessionRequest{
		TenantId: string(stack.TenantA.ID), Title: "a", Labels: map[string]string{"student": "jacob"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	sid := sess.Msg.GetSession().GetId()

	// Bob cannot see Alice's session.
	if _, err := bob.Sessions.GetSession(ctx, connect.NewRequest(&corev1.GetSessionRequest{SessionId: sid})); err == nil {
		t.Fatal("cross-tenant get session succeeded")
	} else {
		var ce *connect.Error
		if !errors.As(err, &ce) || ce.Code() != connect.CodeNotFound {
			t.Fatalf("want NotFound, got %v", err)
		}
	}
	// Label selector matching Alice's labels returns empty for Bob, not error.
	list, err := bob.Sessions.ListSessions(ctx, connect.NewRequest(&corev1.ListSessionsRequest{
		TenantId:       string(stack.TenantB.ID),
		LabelSelectors: []*corev1.LabelSelector{{Key: "student", Op: "=", Values: []string{"jacob"}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Msg.GetSessions()) != 0 {
		t.Fatalf("cross-tenant label query leaked %d sessions", len(list.Msg.GetSessions()))
	}
	// Event subscribe is not-found (error may surface on first receive for
	// streaming RPCs).
	sub, err := bob.Subscribe(ctx, sid, 0)
	if err == nil {
		_, err = sub.Next()
		sub.Close()
	}
	if err == nil {
		t.Fatal("cross-tenant subscribe succeeded")
	}

	// Credentials and provider instances are also tenant-scoped.
	if _, err := bob.Credentials.ListCredentials(ctx, connect.NewRequest(&corev1.ListCredentialsRequest{
		TenantId: string(stack.TenantA.ID),
	})); err == nil {
		t.Fatal("cross-tenant list credentials succeeded")
	}
	if _, err := bob.Providers.ListProviders(ctx, connect.NewRequest(&corev1.ListProvidersRequest{
		TenantId: string(stack.TenantA.ID),
	})); err == nil {
		t.Fatal("cross-tenant list providers succeeded")
	}
}

// TestA33_KeyLifecycle covers A3.3: revoke fails closed mid-stream; sessions
// scope cannot manage tenants/keys; raw keys never appear in events/logs.
func TestA33_KeyLifecycle(t *testing.T) {
	stack := harness.Up(t)
	ctx := context.Background()
	alice := stack.AliceClient()

	// Sessions-scoped key.
	created, err := alice.Tenants.CreateAPIKey(ctx, connect.NewRequest(&corev1.CreateAPIKeyRequest{
		TenantId: string(stack.TenantA.ID), Name: "sess", Scope: corev1.KeyScope_KEY_SCOPE_SESSIONS,
	}))
	if err != nil {
		t.Fatal(err)
	}
	raw := created.Msg.GetRawKey()
	if raw == "" || created.Msg.GetKey().GetPrefix() == "" {
		t.Fatal("expected raw key and prefix")
	}
	// List surface is secret-free: prefix only, never the raw key.
	listed, err := alice.Tenants.ListAPIKeys(ctx, connect.NewRequest(&corev1.ListAPIKeysRequest{
		TenantId: string(stack.TenantA.ID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range listed.Msg.GetKeys() {
		if k.GetId() == created.Msg.GetKey().GetId() && k.GetPrefix() == "" {
			t.Fatal("listed key missing prefix")
		}
		// Proto has no raw field; defend against accidental string leak in name.
		if strings.Contains(k.GetName(), raw) || strings.Contains(k.GetPrefix(), raw) {
			t.Fatalf("list leaked raw key material: %+v", k)
		}
	}
	sessClient := testclient.New(stack.BaseURL, raw)

	// Sessions key can create a session.
	if _, err := sessClient.Sessions.CreateSession(ctx, connect.NewRequest(&corev1.CreateSessionRequest{
		TenantId: string(stack.TenantA.ID), Title: "ok",
	})); err != nil {
		t.Fatal(err)
	}
	// But cannot create keys or tenants.
	if _, err := sessClient.Tenants.CreateAPIKey(ctx, connect.NewRequest(&corev1.CreateAPIKeyRequest{
		TenantId: string(stack.TenantA.ID), Name: "x", Scope: corev1.KeyScope_KEY_SCOPE_SESSIONS,
	})); err == nil {
		t.Fatal("sessions key created a key")
	}
	if _, err := sessClient.Tenants.CreateTenant(ctx, connect.NewRequest(&corev1.CreateTenantRequest{Name: "nope"})); err == nil {
		t.Fatal("sessions key created a tenant")
	}

	// Open a session and subscribe, then revoke mid-stream.
	sess, err := alice.Sessions.CreateSession(ctx, connect.NewRequest(&corev1.CreateSessionRequest{
		TenantId: string(stack.TenantA.ID), Title: "stream",
	}))
	if err != nil {
		t.Fatal(err)
	}
	sid := sess.Msg.GetSession().GetId()
	sub, err := sessClient.Subscribe(ctx, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	if _, err := alice.Tenants.RevokeAPIKey(ctx, connect.NewRequest(&corev1.RevokeAPIKeyRequest{
		TenantId: string(stack.TenantA.ID), KeyId: created.Msg.GetKey().GetId(),
	})); err != nil {
		t.Fatal(err)
	}

	// Mid-stream: either Next fails closed or a subsequent unary fails closed.
	deadline := time.Now().Add(5 * time.Second)
	streamClosed := false
	for time.Now().Before(deadline) {
		if !streamClosed {
			done := make(chan error, 1)
			go func() { _, err := sub.Next(); done <- err }()
			select {
			case err := <-done:
				if err != nil {
					streamClosed = true
				}
			case <-time.After(200 * time.Millisecond):
			}
		}
		_, err := sessClient.Sessions.GetSession(ctx, connect.NewRequest(&corev1.GetSessionRequest{SessionId: sid}))
		if err != nil {
			var ce *connect.Error
			if errors.As(err, &ce) && ce.Code() == connect.CodeUnauthenticated {
				if !streamClosed {
					// Unary closed; stream should follow on next poll tick.
					time.Sleep(1200 * time.Millisecond)
					if _, err := sub.Next(); err == nil {
						t.Fatal("open subscribe survived key revocation")
					}
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("revoked key still authenticated after timeout")
}

// TestA34_ActorAttribution covers A3.4: API-caused events carry the caller's
// Actor; runs store that Actor; loop-internal events use agent/system kinds.
func TestA34_ActorAttribution(t *testing.T) {
	stack := harness.Up(t)
	ctx := context.Background()
	alice := testclient.NewWithActor(stack.BaseURL, stack.KeyA, "student/jacob/SmFjb2I") // display base64url "Jacob"

	sess, err := alice.Sessions.CreateSession(ctx, connect.NewRequest(&corev1.CreateSessionRequest{
		TenantId: string(stack.TenantA.ID), Title: "attr",
	}))
	if err != nil {
		t.Fatal(err)
	}
	sid := sess.Msg.GetSession().GetId()
	seq, err := alice.AppendUserMessage(ctx, sid, "hi")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := alice.Subscribe(ctx, sid, seq-1)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	evs := sub.Collect(t, 1, 5*time.Second)
	actor := evs[0].GetActor()
	if actor.GetKind() != "student" || actor.GetId() != "jacob" {
		t.Fatalf("actor = %+v, want student/jacob", actor)
	}

	// StartRun must capture the caller's Actor on the run row.
	run, err := alice.Agents.StartRun(ctx, connect.NewRequest(&corev1.StartRunRequest{
		SessionId: sid, Prompt: "attribute me",
		Policy: &corev1.RunPolicy{AllowTools: []string{"post_event"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := stack.Store.Tenant(stack.TenantA.ID).Runs().Get(ctx, uc.RunID(run.Msg.GetRun().GetId()))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Actor.Kind != "student" || stored.Actor.ID != "jacob" {
		t.Fatalf("run actor = %+v, want student/jacob", stored.Actor)
	}
	if run.Msg.GetRun().GetActorKind() != "student" || run.Msg.GetRun().GetActorId() != "jacob" {
		t.Fatalf("proto run actor = %s/%s", run.Msg.GetRun().GetActorKind(), run.Msg.GetRun().GetActorId())
	}
}
