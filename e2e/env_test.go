package e2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/mcp"
	"github.com/aleksclark/ultracore/provider/localdocker"
	"github.com/aleksclark/ultracore/secrets"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/modelscript"
)

func provisionEnv(t *testing.T, stack *harness.Stack, session string) *corev1.Resource {
	t.Helper()
	client := stack.AliceClient()
	resp, err := client.Resources.ProvisionResource(context.Background(), connect.NewRequest(&corev1.ProvisionResourceRequest{
		SessionId:        session,
		Spec:             &corev1.DevEnvSpec{Name: "main", Workdir: "/work"},
		ProviderInstance: "default",
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := resp.Msg.GetResource().GetId()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		got, err := client.Resources.GetResource(context.Background(), connect.NewRequest(&corev1.GetResourceRequest{ResourceId: id}))
		if err == nil {
			switch got.Msg.GetResource().GetState() {
			case corev1.ResourceState_RESOURCE_STATE_READY:
				return got.Msg.GetResource()
			case corev1.ResourceState_RESOURCE_STATE_FAILED:
				t.Fatalf("env failed: %s", got.Msg.GetResource().GetFailureMessage())
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
	sess := createSession(t, alice, string(stack.TenantA.ID), "env work")
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
	alice.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 60*time.Second)
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
	alice.AwaitRunState(t, run2.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 60*time.Second)
}

// A2.4/A2.5/A2.7 — auth, reconciliation, and encrypted tokens.
func TestA24_A25_A27_AuthReconcileMetering(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, alice, string(stack.TenantA.ID), "reconcile")
	envProto := provisionEnv(t, stack, sess.GetId())
	env, err := stack.Store.Tenant(stack.TenantA.ID).Resources().Get(ctx, uc.ResourceID(envProto.GetId()))
	if err != nil {
		t.Fatal(err)
	}
	if err := mcp.NewClient(string(env.Endpoint), "wrong").Initialize(ctx); err == nil {
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
	defer func() { _ = provider.Close() }()
	if err := provider.KillByResourceID(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := stack.Store.Tenant(stack.TenantA.ID).Resources().Get(ctx, env.ID)
		if current.State == uc.ResourceFailed {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	current, _ := stack.Store.Tenant(stack.TenantA.ID).Resources().Get(ctx, env.ID)
	if current.State != uc.ResourceFailed {
		t.Fatalf("state=%s", current.State)
	}
}
