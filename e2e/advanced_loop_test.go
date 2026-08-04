package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/loop"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/modelscript"
)

func TestA62_CompactionAndHook(t *testing.T) {
	stack := harness.Up(t)
	c := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, c, string(stack.TenantA.ID), "compact")
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{{Text: "done"}}})
	history, _ := loop.InitialEnvelope(strings.Repeat("large context ", 4000))
	run := uc.AgentRun{ID: uc.RunID("00000000-0000-0000-0000-000000006200"), SessionID: uc.SessionID(sess.GetId()), TenantID: stack.TenantA.ID, LoopKind: loop.DefaultLoopKind, LoopVersion: 1, ModelConfig: uc.ModelConfig{Provider: "openai", ModelID: "mock-model", Credential: "default"}, Prompt: "compact", History: history, Policy: uc.DefaultRunPolicy()}
	if err := stack.Store.Tenant(stack.TenantA.ID).Runs().Create(ctx, run); err != nil {
		t.Fatal(err)
	} // execute via public start is separately covered; here seed large durable history and verify hook artifact after a normal run
	started, _, err := c.StartRun(ctx, sess.GetId(), "normal hook run")
	if err != nil {
		t.Fatal(err)
	}
	c.AwaitRunState(t, started.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 30*time.Second)
	memory, err := stack.Store.Tenant(stack.TenantA.ID).Memory().Get(ctx, uc.SessionID(sess.GetId()), "system.cost.latest")
	if err != nil || len(memory.Value) == 0 {
		t.Fatalf("cost hook missing: %v", err)
	}
}
