package e2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envprovider/localdocker"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/mcp"
	"github.com/aleksclark/ultralogical/secrets"
	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
)

func provisionEnv(t *testing.T, stack *harness.Stack, session string) *ultrav1.DevEnv {
	t.Helper()
	client := stack.AliceClient()
	resp, err := client.Envs.ProvisionEnv(context.Background(), connect.NewRequest(&ultrav1.ProvisionEnvRequest{
		SessionId:        session,
		Spec:             &ultrav1.EnvSpec{Name: "main", Workdir: "/work"},
		ProviderInstance: "default",
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := resp.Msg.GetEnv().GetId()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		got, err := client.Envs.GetEnv(context.Background(), connect.NewRequest(&ultrav1.GetEnvRequest{EnvId: id}))
		if err == nil {
			switch got.Msg.GetEnv().GetState() {
			case ultrav1.EnvState_ENV_STATE_READY:
				return got.Msg.GetEnv()
			case ultrav1.EnvState_ENV_STATE_FAILED:
				t.Fatalf("env failed: %s", got.Msg.GetEnv().GetFailureMessage())
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("env never ready")
	return nil
}

// A2.2/A2.3 — real env work persists across agent runs and worker restart.
func TestA22_A23_AgentEnvPersistence(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, alice, string(stack.OrgA.ID), "env work")
	_ = provisionEnv(t, stack, sess.GetId())
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{ToolCalls: []modelscript.ToolCallSpec{{Name: "bash", Args: map[string]any{"command": "git init && echo hi > README.md"}}}},
		{ToolCalls: []modelscript.ToolCallSpec{{Name: "view", Args: map[string]any{"file_path": "/work/README.md"}}}},
		{Text: "done"},
	}})
	run, _, err := alice.StartRun(ctx, sess.GetId(), "create README")
	if err != nil {
		t.Fatal(err)
	}
	alice.AwaitRunState(t, run.GetId(), ultrav1.RunState_RUN_STATE_COMPLETED, 60*time.Second)
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{ToolCalls: []modelscript.ToolCallSpec{{Name: "view", Args: map[string]any{"file_path": "/work/README.md"}}}},
		{Text: "still here"},
	}})
	stack.KillWorker()
	stack.StartWorker()
	run2, _, err := alice.StartRun(ctx, sess.GetId(), "read README")
	if err != nil {
		t.Fatal(err)
	}
	alice.AwaitRunState(t, run2.GetId(), ultrav1.RunState_RUN_STATE_COMPLETED, 60*time.Second)
}

// A2.4/A2.5/A2.7 — auth, reconciliation, encrypted tokens, metering.
func TestA24_A25_A27_AuthReconcileMetering(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, alice, string(stack.OrgA.ID), "meter")
	envProto := provisionEnv(t, stack, sess.GetId())
	env, err := stack.Store.Org(stack.OrgA.ID).Envs().Get(ctx, ultra.EnvID(envProto.GetId()))
	if err != nil {
		t.Fatal(err)
	}
	if err := mcp.NewClient(env.Endpoint, "wrong").Initialize(ctx); err == nil {
		t.Fatal("wrong token authenticated")
	}
	keyring, err := secrets.NewAESKeyring(stack.MasterKey)
	if err != nil {
		t.Fatal(err)
	}
	clear, err := keyring.Decrypt(env.TokenEnc)
	if err != nil {
		t.Fatal(err)
	}
	if json.Valid(env.TokenEnc) || string(env.TokenEnc) == string(clear) {
		t.Fatal("token not encrypted")
	}
	provider, err := localdocker.New(localdocker.Config{Image: harness.BezalelImage})
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	if err := provider.KillByEnvID(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := stack.Store.Org(stack.OrgA.ID).Envs().Get(ctx, env.ID)
		if current.State == ultra.EnvFailed {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	current, _ := stack.Store.Org(stack.OrgA.ID).Envs().Get(ctx, env.ID)
	if current.State != ultra.EnvFailed {
		t.Fatalf("state=%s", current.State)
	}
	usage, err := stack.Store.Org(stack.OrgA.ID).Usage().List(ctx, time.Time{}, time.Now().Add(time.Hour))
	if err != nil || len(usage) != 1 {
		t.Fatalf("usage=%v err=%v", usage, err)
	}
	if usage[0].EndedAt == nil || usage[0].RateClass != ultra.RateClassBYO {
		t.Fatalf("bad interval: %+v", usage[0])
	}
}
