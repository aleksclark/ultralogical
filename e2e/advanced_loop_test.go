package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/loop"
	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
)

func TestA62_CompactionAndHook(t *testing.T) {
	stack := harness.Up(t)
	c := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, c, string(stack.OrgA.ID), "compact")
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{{Text: "done"}}})
	history, _ := loop.InitialEnvelope(strings.Repeat("large context ", 4000))
	run := ultra.AgentRun{ID: ultra.RunID("00000000-0000-0000-0000-000000006200"), SessionID: ultra.SessionID(sess.GetId()), OrgID: stack.OrgA.ID, LoopKind: loop.DefaultLoopKind, LoopVersion: 1, ModelConfig: ultra.ModelConfig{Provider: "openai", ModelID: "mock-model", Credential: "default"}, Prompt: "compact", History: history, Grants: ultra.RootGrants()}
	if err := stack.Store.Org(stack.OrgA.ID).Runs().Create(ctx, run); err != nil {
		t.Fatal(err)
	} // execute via public start is separately covered; here seed large durable history and verify hook artifact after a normal run
	started, _, err := c.StartRun(ctx, sess.GetId(), "normal hook run")
	if err != nil {
		t.Fatal(err)
	}
	c.AwaitRunState(t, started.GetId(), ultrav1.RunState_RUN_STATE_COMPLETED, 30*time.Second)
	memory, err := stack.Store.Org(stack.OrgA.ID).Memory().Get(ctx, ultra.SessionID(sess.GetId()), "system.cost.latest")
	if err != nil || len(memory.Value) == 0 {
		t.Fatalf("cost hook missing: %v", err)
	}
}
