package e2e

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/testkit/harness"
)

// A4.1/A4.2 — a flow invocation runs its declared agent with the rendered
// prompt and full provenance, and a later version cannot change it.
//
// Phase 9 completed this capability: a flow's agents start only once its
// declared environments are ready, so an invocation reports no runs at accept
// time and a client follows the invocation instead. The deeper assertions
// (readiness gating, topology, convergence, cancellation) live in the A9 suite.
func TestA41_A42_FlowInvocationAndVersioning(t *testing.T) {
	stack := harness.Up(t)
	c := stack.AliceClient()
	stack.Model.SetScript(flowScript())
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	definition := `{"params":{"subject":{"type":"string","required":true}},` +
		`"agents":{"entry":{"prompt":"flow reviewer: {{.subject}}","entry":true,` +
		`"model":{"provider":"openai","model_id":"mock-model","credential":"default"},"tools":["post_event"]}}}`
	v1 := putFlow(t, c, org, "review", definition)
	if v1.GetVersion() != 1 {
		t.Fatalf("first version = %d", v1.GetVersion())
	}

	sess := createSession(t, c, org, "flow")
	inv := invokeFlow(t, c, sess.GetId(), "review", `{"subject":"database"}`)
	final := invocationTerminal(t, c, inv.GetInvocationId(), 90*time.Second)
	if final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED {
		t.Fatalf("state = %v reason %q", final.GetState(), final.GetTerminalReason())
	}
	if len(final.GetRuns()) != 1 {
		t.Fatalf("want 1 run, got %d", len(final.GetRuns()))
	}
	runID := final.GetRuns()[0].GetRunId()
	c.AwaitRunState(t, runID, ultrav1.RunState_RUN_STATE_COMPLETED, 30*time.Second)

	stored, err := stack.Store.Org(stack.OrgA.ID).Runs().Get(ctx, ultra.RunID(runID))
	if err != nil {
		t.Fatal(err)
	}
	if stored.FlowInvocationID == nil || string(*stored.FlowInvocationID) != inv.GetInvocationId() {
		t.Fatal("flow provenance missing")
	}
	if stored.Prompt != "flow reviewer: database" {
		t.Fatalf("rendered prompt=%q", stored.Prompt)
	}

	// A second version does not disturb the first, which stays readable and
	// byte-identical forever.
	definition2 := `{"agents":{"entry":{"prompt":"flow reviewer: new prompt","entry":true,"tools":["post_event"]}}}`
	v2 := putFlow(t, c, org, "review", definition2)
	if v2.GetVersion() != 2 {
		t.Fatalf("v2 = %d", v2.GetVersion())
	}
	old, err := c.Flows.GetFlow(ctx, connect.NewRequest(&ultrav1.GetFlowRequest{
		OrgId: org, Name: "review", Version: 1,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if old.Msg.GetFlow().GetVersion() != 1 || old.Msg.GetFlow().GetDefinitionJson() != definition {
		t.Fatal("v1 not pinned")
	}
}

// A4.4 — an invalid definition is refused at write time, with typed field
// paths, and nothing is persisted.
func TestA44_FlowValidation(t *testing.T) {
	stack := harness.Up(t)
	c := stack.AliceClient()
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	_, err := c.Flows.PutFlow(ctx, connect.NewRequest(&ultrav1.PutFlowRequest{
		OrgId: org, Name: "bad", DefinitionJson: `{"agents":{"x":{"prompt":"{{","entry":true}}}`,
	}))
	if err == nil {
		t.Fatal("invalid template accepted")
	}
	if fields := flowFieldErrors(t, err); fields["agents.x.prompt"] != ultra.FlowErrInvalidTemplate {
		t.Fatalf("field errors = %v", fields)
	}
	listed, err := c.Flows.ListFlows(ctx, connect.NewRequest(&ultrav1.ListFlowsRequest{OrgId: org}))
	if err != nil {
		t.Fatal(err)
	}
	for _, flow := range listed.Msg.GetFlows() {
		if flow.GetName() == "bad" {
			t.Fatal("a rejected definition was persisted")
		}
	}
}
