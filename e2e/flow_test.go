package e2e

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
)

func TestA41_A42_FlowInvocationAndVersioning(t *testing.T) {
	stack := harness.Up(t)
	c := stack.AliceClient()
	ctx := context.Background()
	definition := `{"params":{"subject":{"type":"string","required":true}},"agents":{"entry":{"prompt":"Review {{.subject}}","entry":true,"model":{"provider":"openai","model_id":"mock-model","credential":"default"}}}}`
	v1, err := c.Flows.PutFlow(ctx, connect.NewRequest(&ultrav1.PutFlowRequest{OrgId: string(stack.OrgA.ID), Name: "review", DefinitionJson: definition}))
	if err != nil {
		t.Fatal(err)
	}
	if v1.Msg.GetFlow().GetVersion() != 1 {
		t.Fatal("bad version")
	}
	sess := createSession(t, c, string(stack.OrgA.ID), "flow")
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{{Text: "flow done"}}})
	inv, err := c.Flows.InvokeFlow(ctx, connect.NewRequest(&ultrav1.InvokeFlowRequest{SessionId: sess.GetId(), Name: "review", ParamsJson: `{"subject":"database"}`}))
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Msg.GetRunIds()) != 1 {
		t.Fatal("no entry run")
	}
	c.AwaitRunState(t, inv.Msg.GetRunIds()[0], ultrav1.RunState_RUN_STATE_COMPLETED, 30*time.Second)
	stored, err := stack.Store.Org(stack.OrgA.ID).Runs().Get(ctx, ultra.RunID(inv.Msg.GetRunIds()[0]))
	if err != nil {
		t.Fatal(err)
	}
	if stored.FlowInvocationID == nil || string(*stored.FlowInvocationID) != inv.Msg.GetInvocationId() {
		t.Fatal("flow provenance missing")
	}
	if stored.Prompt != "Review database" {
		t.Fatalf("rendered prompt=%q", stored.Prompt)
	}
	definition2 := `{"agents":{"entry":{"prompt":"New prompt","entry":true}}}`
	v2, err := c.Flows.PutFlow(ctx, connect.NewRequest(&ultrav1.PutFlowRequest{OrgId: string(stack.OrgA.ID), Name: "review", DefinitionJson: definition2}))
	if err != nil || v2.Msg.GetFlow().GetVersion() != 2 {
		t.Fatalf("v2=%v err=%v", v2, err)
	}
	old, err := c.Flows.GetFlow(ctx, connect.NewRequest(&ultrav1.GetFlowRequest{OrgId: string(stack.OrgA.ID), Name: "review", Version: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if old.Msg.GetFlow().GetVersion() != 1 || old.Msg.GetFlow().GetDefinitionJson() == definition2 {
		t.Fatal("v1 not pinned")
	}
}
func TestA44_FlowValidation(t *testing.T) {
	stack := harness.Up(t)
	c := stack.AliceClient()
	_, err := c.Flows.PutFlow(context.Background(), connect.NewRequest(&ultrav1.PutFlowRequest{OrgId: string(stack.OrgA.ID), Name: "bad", DefinitionJson: `{"agents":{"x":{"prompt":"{{","entry":true}}}}`}))
	if err == nil {
		t.Fatal("invalid template accepted")
	}
}
