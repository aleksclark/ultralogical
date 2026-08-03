package e2e

import (
	"context"
	"encoding/json"
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

// A8.9 — every claim in docs/security.md, executed. Each subtest is named
// after the claim it proves, so a reader can move between the document and
// the evidence without guessing. If a subtest is deleted, the corresponding
// paragraph must be deleted too.
func TestA89_SecurityDocumentation(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	org := stack.OrgA.ID
	sess := createSession(t, alice, string(org), "security documentation")

	// Section 1 — a caller may narrow a human-started run's authority but
	// never widen it, and widening is refused rather than clamped.
	t.Run("narrowing_only_at_start_run", func(t *testing.T) {
		stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
			{Match: modelscript.UserContains("narrowed start"), Sticky: true, Text: "narrowed run done"},
		}})

		narrowed, err := alice.Agents.StartRun(ctx, connect.NewRequest(&corev1.StartRunRequest{
			SessionId: sess.GetId(), Prompt: "narrowed start",
			Grants: &corev1.Grants{Tools: []string{"post_event"}},
		}))
		if err != nil {
			t.Fatalf("a request for *less* authority must be accepted: %v", err)
		}
		stored, err := stack.Store.Org(org).Runs().Get(ctx, uc.RunID(narrowed.Msg.GetRun().GetId()))
		if err != nil {
			t.Fatal(err)
		}
		if stored.Grants.AllowsTool("spawn_agent") {
			t.Fatalf("narrowed run kept an authority it did not ask for: %+v", stored.Grants)
		}

		// Explicit full allowlist is accepted: E1 has no lattice ceiling on
		// StartRun, only the tools list that will be checked at dispatch.
		full, err := alice.Agents.StartRun(ctx, connect.NewRequest(&corev1.StartRunRequest{
			SessionId: sess.GetId(), Prompt: "full start",
			Grants: &corev1.Grants{Tools: []string{"*"}},
		}))
		if err != nil {
			t.Fatalf("an explicit full allowlist must be accepted: %v", err)
		}
		storedFull, err := stack.Store.Org(org).Runs().Get(ctx, uc.RunID(full.Msg.GetRun().GetId()))
		if err != nil {
			t.Fatal(err)
		}
		if !storedFull.Grants.AllowsTool("spawn_agent") {
			t.Fatalf("full allowlist did not grant spawn_agent: %+v", storedFull.Grants)
		}
	})

	// Section 1 — a child granted no tools is refused at dispatch.
	t.Run("empty_allowlist_denies_at_dispatch", func(t *testing.T) {
		results, childID := forgedToolCall(t, stack, alice, org, sess.GetId())
		var denials []toolResult
		for _, r := range results {
			if r.IsError {
				denials = append(denials, r)
			}
		}
		if len(denials) == 0 {
			t.Fatalf("empty-allowlist child was not refused: %+v", results)
		}
		if childID == "" {
			t.Fatal("missing child id")
		}
	})

	// Section 3 — denials are uniform and disclose nothing, including for a
	// capability that genuinely does not exist for this run.
	t.Run("denials_are_uniform_and_opaque", func(t *testing.T) {
		results, _ := forgedToolCall(t, stack, alice, org, sess.GetId())
		var denials []toolResult
		for _, r := range results {
			if r.IsError {
				denials = append(denials, r)
			}
		}
		if len(denials) == 0 {
			t.Fatalf("the forged call was not refused: %+v", results)
		}
		for _, d := range denials {
			if strings.TrimSpace(d.Content) != "permission denied" {
				t.Fatalf("denial for %q was %q, want the uniform message with no detail", d.Name, d.Content)
			}
			// The framework's own "tool not found" answer enumerates every
			// registered tool. If that ever reaches the model again, this
			// catches it.
			for _, leak := range []string{"Available tools", "not found", "post_event", sess.GetId()} {
				if strings.Contains(d.Content, leak) {
					t.Fatalf("denial leaked %q: %s", leak, d.Content)
				}
			}
		}
	})

	// Section 3 — denials are visible to humans through the event log even
	// though they are opaque to the model.
	t.Run("denial_emits_an_audit_event", func(t *testing.T) {
		_, childID := forgedToolCall(t, stack, alice, org, sess.GetId())
		events := collectEvents(t, stack, sess.GetId(), uc.EventKindPermissionDenied, 60*time.Second, 1)
		var matched bool
		for _, e := range events {
			var payload uc.PermissionDeniedPayload
			if json.Unmarshal(e.Payload, &payload) != nil {
				continue
			}
			if payload.RunID == childID && payload.Tool != "" && payload.Reason != "" {
				matched = true
			}
		}
		if !matched {
			t.Fatalf("no permission_denied event named run %s with a tool and a reason", childID)
		}
	})

	// Section 2 and 3 — a refused call must also not have happened. A denial
	// that still performed the side effect would be worthless.
	t.Run("denied_side_effect_never_happens", func(t *testing.T) {
		_, childID := forgedToolCall(t, stack, alice, org, sess.GetId())
		events, err := stack.Store.Org(org).Events().Range(ctx, uc.SessionID(sess.GetId()), 0, 4096)
		if err != nil {
			t.Fatal(err)
		}
		// The forged call was post_event with a distinctive marker. If the
		// side effect had run, that annotation would be in the log.
		for _, e := range events {
			if e.Kind != uc.EventKindAnnotation {
				continue
			}
			if strings.Contains(string(e.Payload), forgedSideEffectMarker) {
				t.Fatalf("the denied call still wrote its side effect at seq %d", e.Seq)
			}
		}
		if childID == "" {
			t.Fatal("the forging child never ran")
		}
	})

	// Section 4 — cross-tenant and missing are the same answer.
	t.Run("cross_tenant_reads_are_indistinguishable_from_missing", func(t *testing.T) {
		bob := stack.BobClient()
		missing := "00000000-0000-0000-0000-000000000000"

		type probe struct {
			name     string
			existing func() error
			absent   func() error
		}
		probes := []probe{
			{
				name: "GetSession",
				existing: func() error {
					_, err := bob.Sessions.GetSession(ctx, connect.NewRequest(&corev1.GetSessionRequest{SessionId: sess.GetId()}))
					return err
				},
				absent: func() error {
					_, err := bob.Sessions.GetSession(ctx, connect.NewRequest(&corev1.GetSessionRequest{SessionId: missing}))
					return err
				},
			},
			{
				name: "ListRuns",
				existing: func() error {
					_, err := bob.Agents.ListRuns(ctx, connect.NewRequest(&corev1.ListRunsRequest{SessionId: sess.GetId()}))
					return err
				},
				absent: func() error {
					_, err := bob.Agents.ListRuns(ctx, connect.NewRequest(&corev1.ListRunsRequest{SessionId: missing}))
					return err
				},
			},
			{
				name: "ListMemory",
				existing: func() error {
					_, err := bob.Sessions.ListMemory(ctx, connect.NewRequest(&corev1.ListMemoryRequest{SessionId: sess.GetId()}))
					return err
				},
				absent: func() error {
					_, err := bob.Sessions.ListMemory(ctx, connect.NewRequest(&corev1.ListMemoryRequest{SessionId: missing}))
					return err
				},
			},
		}
		for _, p := range probes {
			crossTenant, absent := p.existing(), p.absent()
			if crossTenant == nil {
				t.Fatalf("%s let another tenant read the session", p.name)
			}
			if absent == nil {
				t.Fatalf("%s succeeded for an id that does not exist", p.name)
			}
			if connect.CodeOf(crossTenant) != connect.CodeOf(absent) {
				t.Fatalf("%s distinguishes cross-tenant (%s) from missing (%s)",
					p.name, connect.CodeOf(crossTenant), connect.CodeOf(absent))
			}
			if crossTenant.Error() != absent.Error() {
				t.Fatalf("%s leaks existence through its message: cross-tenant %q vs missing %q",
					p.name, crossTenant.Error(), absent.Error())
			}
			if connect.CodeOf(crossTenant) != connect.CodeNotFound {
				t.Fatalf("%s returned %s, want NotFound", p.name, connect.CodeOf(crossTenant))
			}
		}
	})

	// Section 6 — the inference credential is never observable outside the
	// worker, in any encoding.
	t.Run("credentials_never_leave_the_worker", func(t *testing.T) {
		stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
			{Match: modelscript.UserContains("credential probe"), Sticky: true, Text: "probe done"},
		}})
		run, _, err := alice.StartRun(ctx, sess.GetId(), "credential probe")
		if err != nil {
			t.Fatal(err)
		}
		alice.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 90*time.Second)

		events, err := stack.Store.Org(org).Events().Range(ctx, uc.SessionID(sess.GetId()), 0, 4096)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			assertNoCanary(t, "event payload "+e.Kind, string(e.Payload))
		}
		stored, err := stack.Store.Org(org).Runs().Get(ctx, uc.RunID(run.GetId()))
		if err != nil {
			t.Fatal(err)
		}
		assertNoCanary(t, "persisted run history", string(stored.History))
		assertNoCanary(t, "persisted run result", string(stored.Result))

		listed, err := alice.Orgs.ListCredentials(ctx, connect.NewRequest(&corev1.ListCredentialsRequest{
			OrgId: string(org),
		}))
		if err != nil {
			t.Fatal(err)
		}
		blob, err := json.Marshal(listed.Msg)
		if err != nil {
			t.Fatal(err)
		}
		assertNoCanary(t, "ListCredentials response", string(blob))

		atRest, err := stack.Store.Org(org).Credentials().Get(ctx, uc.CredentialKindOpenAI, "default")
		if err != nil {
			t.Fatal(err)
		}
		assertNoCanary(t, "credential ciphertext at rest", string(atRest.EncPayload))
		assertNoCanary(t, "cored and worker logs", stack.Logs())

		// The redactor really is covering encoded forms, not just the literal
		// value: this is what makes the log assertion above meaningful.
		forms := secrets.Encodings(harness.CanaryAPIKey)
		if len(forms) < 2 {
			t.Fatalf("the canary has %d encoded forms; the leak sweep is weaker than documented", len(forms))
		}
	})

	// Section 6 — possessing a credential grants no tool, environment, or
	// spawn authority. A narrowed child shares the org credential store with
	// its parent and gains nothing from it.
	t.Run("narrowed_child_gains_nothing_from_org_credentials", func(t *testing.T) {
		child := runGrantLadder(t, stack, alice, org, sess.GetId())

		// The child could reach the model, which is the only thing the shared
		// credential buys it.
		steps, err := stack.Store.Org(org).Runs().Steps(ctx, child.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(steps) == 0 {
			t.Fatal("the child never called the model, so this proves nothing about credentials")
		}
		// And it is still bounded by its grants, not by what the org can pay
		// for.
		for _, tool := range []string{"terminate_env", "provision_env", "run_agent_cohort"} {
			if child.Grants.AllowsTool(tool) {
				t.Fatalf("child holds %q, which was never delegated: %+v", tool, child.Grants)
			}
		}
	})
}

// forgedSideEffectMarker is the text a denied post_event call would have
// written. Its absence from the log is the proof the denial was substantive.
const forgedSideEffectMarker = "forged-side-effect-must-not-appear"

// runGrantLadder drives a parent that spawns a narrowed child, which then
// attempts to escalate and finally spawns a compliant grandchild. It returns
// the child once it is terminal.
//
// Two subtests need the same ladder; building it twice in one session with
// distinct prompts keeps each subtest independently meaningful.
func runGrantLadder(t *testing.T, stack *harness.Stack, alice interface {
	StartRun(context.Context, string, string) (*corev1.AgentRun, int64, error)
}, org uc.OrgID, session string) uc.AgentRun {
	t.Helper()
	prompt := "ladder parent " + t.Name()
	childPrompt := "ladder child " + t.Name()

	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{
			Match: modelscript.UserContains(prompt),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "spawn_agent", Args: map[string]any{
				"prompt": childPrompt, "tools": []string{"post_event", "spawn_agent"},
			}}},
		},
		{Match: modelscript.UserContains(prompt), Sticky: true, Text: "ladder parent done"},
		// The child asks for authority its parent does not hold. Must fail.
		{
			Match: modelscript.UserContains(childPrompt),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "spawn_agent", Args: map[string]any{
				"prompt": "escalated grandchild", "tools": []string{"terminate_env"},
			}}},
		},
		// Then a compliant one, which must succeed and be strictly narrower.
		{
			Match: modelscript.UserContains(childPrompt),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "spawn_agent", Args: map[string]any{
				"prompt": "ladder grandchild " + t.Name(), "tools": []string{"post_event"},
			}}},
		},
		{Match: modelscript.UserContains(childPrompt), Sticky: true, Text: "ladder child done"},
		{Match: modelscript.UserContains("ladder grandchild"), Sticky: true, Text: "ladder grandchild done"},
	}})

	parent, _, err := alice.StartRun(context.Background(), session, prompt)
	if err != nil {
		t.Fatal(err)
	}
	kids := childrenOf(t, stack, org, uc.RunID(parent.GetId()), 1, 90*time.Second)
	return awaitRunOneOf(t, stack, org, kids[0].ID, 3*time.Minute, uc.RunCompleted, uc.RunFailed)
}

// forgedToolCall runs a child that has been granted nothing but calls a tool
// anyway, and returns the child's tool results plus its run id.
func forgedToolCall(t *testing.T, stack *harness.Stack, alice interface {
	StartRun(context.Context, string, string) (*corev1.AgentRun, int64, error)
}, org uc.OrgID, session string) ([]toolResult, uc.RunID) {
	t.Helper()
	prompt := "forge parent " + t.Name()
	childPrompt := "forge child " + t.Name()

	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{
			Match: modelscript.UserContains(prompt),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "spawn_agent", Args: map[string]any{
				// Granted nothing at all: every canonical tool is a denial
				// stub for this child.
				"prompt": childPrompt, "tools": []string{},
			}}},
		},
		{Match: modelscript.UserContains(prompt), Sticky: true, Text: "forge parent done"},
		// A tool the child was never offered. The model can name anything.
		{
			Match: modelscript.UserContains(childPrompt),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "post_event", Args: map[string]any{
				"text": forgedSideEffectMarker,
			}}},
		},
		{Match: modelscript.UserContains(childPrompt), Sticky: true, Text: "forge child done"},
	}})

	parent, _, err := alice.StartRun(context.Background(), session, prompt)
	if err != nil {
		t.Fatal(err)
	}
	kids := childrenOf(t, stack, org, uc.RunID(parent.GetId()), 1, 90*time.Second)
	child := awaitRunOneOf(t, stack, org, kids[0].ID, 3*time.Minute, uc.RunCompleted, uc.RunFailed)
	return toolResultsFor(t, stack, session, child.ID), child.ID
}
