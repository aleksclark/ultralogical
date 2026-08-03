package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/provider/localdocker"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/modelscript"
	"github.com/aleksclark/ultracore/testkit/testclient"
)

// TestResourceKinds_DevEnvAndNullConcurrent proves a session can own mixed
// resource kinds: a tool-serving dev_env and a lifecycle-only null_resource.
// It asserts concurrent readiness, kind-tagged lifecycle events, restart of
// the lifecycle-only kind, dual terminate, and dual leak checks (docker lister
// for dev_env; store terminal + empty active list for null_resource).
func TestResourceKinds_DevEnvAndNullConcurrent(t *testing.T) {
	stack := harness.Up(t)
	ctx := context.Background()
	client := stack.AliceClient()

	_, err := client.Orgs.RegisterProvider(ctx, connect.NewRequest(&corev1.RegisterProviderRequest{
		OrgId:      string(stack.OrgA.ID),
		Name:       "null",
		Kind:       uc.ProviderKindNull,
		ConfigJson: `{}`,
	}))
	if err != nil {
		t.Fatal(err)
	}

	sess := createSession(t, client, string(stack.OrgA.ID), "mixed-kinds")
	sid := sess.GetId()

	dev, err := client.Resources.ProvisionResource(ctx, connect.NewRequest(&corev1.ProvisionResourceRequest{
		SessionId:        sid,
		Kind:             string(uc.ResourceKindDevEnv),
		Spec:             &corev1.DevEnvSpec{Name: "dev", Workdir: "/work"},
		ProviderInstance: "default",
	}))
	if err != nil {
		t.Fatal(err)
	}
	nullResp, err := client.Resources.ProvisionResource(ctx, connect.NewRequest(&corev1.ProvisionResourceRequest{
		SessionId:        sid,
		Kind:             string(uc.ResourceKindNullResource),
		Spec:             &corev1.DevEnvSpec{Name: "null-one"},
		ProviderInstance: "null",
	}))
	if err != nil {
		t.Fatal(err)
	}

	devID := dev.Msg.GetResource().GetId()
	nullID := nullResp.Msg.GetResource().GetId()

	awaitResourceState(t, client, devID, corev1.ResourceState_RESOURCE_STATE_READY, 3*time.Minute)
	nullReady := awaitResourceState(t, client, nullID, corev1.ResourceState_RESOURCE_STATE_READY, 30*time.Second)
	if nullReady.GetEndpoint() != "" {
		t.Fatalf("null_resource must not publish a tool endpoint, got %q", nullReady.GetEndpoint())
	}
	if nullReady.GetKind() != string(uc.ResourceKindNullResource) {
		t.Fatalf("null kind = %q", nullReady.GetKind())
	}

	// ListResources returns both kinds.
	listed, err := client.Resources.ListResources(ctx, connect.NewRequest(&corev1.ListResourcesRequest{SessionId: sid}))
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Msg.GetResources()) != 2 {
		t.Fatalf("list = %d resources, want 2", len(listed.Msg.GetResources()))
	}

	// Restart lifecycle-only kind via API (A2.5).
	if _, err := client.Resources.RestartResource(ctx, connect.NewRequest(&corev1.RestartResourceRequest{ResourceId: nullID})); err != nil {
		t.Fatal(err)
	}
	awaitResourceState(t, client, nullID, corev1.ResourceState_RESOURCE_STATE_READY, 30*time.Second)

	events, err := stack.Store.Org(stack.OrgA.ID).Events().Range(ctx, uc.SessionID(sid), 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var sawDev, sawNull, sawNullRestart bool
	var kindsReady []string
	for _, ev := range events {
		switch ev.Kind {
		case uc.EventKindResourceReady:
			payload := string(ev.Payload)
			kindsReady = append(kindsReady, payload)
			if strings.Contains(payload, devID) {
				sawDev = true
				if !strings.Contains(payload, `"kind":"dev_env"`) && !strings.Contains(payload, `"kind": "dev_env"`) && !strings.Contains(payload, "dev_env") {
					t.Fatalf("dev_env ready event missing kind: %s", payload)
				}
			}
			if strings.Contains(payload, nullID) {
				sawNull = true
				if !strings.Contains(payload, "null_resource") {
					t.Fatalf("null_resource ready event missing kind: %s", payload)
				}
				if strings.Contains(payload, "restarted") || strings.Contains(strings.ToLower(payload), "restart") {
					sawNullRestart = true
				}
			}
		case uc.EventKindResourceRequested, uc.EventKindResourceProvisioning, uc.EventKindResourceTerminating:
			// Interleaved lifecycle vocabulary must carry kind for both.
			payload := string(ev.Payload)
			if strings.Contains(payload, devID) && !strings.Contains(payload, "dev_env") {
				t.Fatalf("%s for dev missing kind: %s", ev.Kind, payload)
			}
			if strings.Contains(payload, nullID) && !strings.Contains(payload, "null_resource") {
				t.Fatalf("%s for null missing kind: %s", ev.Kind, payload)
			}
		}
	}
	if !sawDev || !sawNull {
		t.Fatalf("expected ready events for both resources (dev=%v null=%v) payloads=%v", sawDev, sawNull, kindsReady)
	}
	if !sawNullRestart {
		// Restart still emits ready; message may vary — require at least two
		// ready events mentioning nullID (initial + post-restart).
		n := 0
		for _, p := range kindsReady {
			if strings.Contains(p, nullID) {
				n++
			}
		}
		if n < 2 {
			t.Fatalf("expected null ready before and after restart, got %d ready payloads", n)
		}
	}

	// Lifecycle-only kind via loop native tools (A2.5): list_resources and
	// provision_resource for null_resource (no tool endpoint). Terminate is
	// exercised via ResourceService API below (and restart was already API).
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{ToolCalls: []modelscript.ToolCallSpec{
			{Name: "list_resources", Args: map[string]any{}},
		}},
		{ToolCalls: []modelscript.ToolCallSpec{
			{Name: "provision_resource", Args: map[string]any{
				"kind": string(uc.ResourceKindNullResource), "name": "null-two", "provider_instance": "null",
			}},
		}},
		{Text: "lifecycle-only tools exercised"},
	}})
	run, _, err := client.StartRun(ctx, sid, "manage lifecycle-only resources")
	if err != nil {
		t.Fatal(err)
	}
	client.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 90*time.Second)

	events, err = stack.Store.Org(stack.OrgA.ID).Events().Range(ctx, uc.SessionID(sid), 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var sawListBoth, sawProvisionOK bool
	for _, ev := range events {
		if ev.Kind != uc.EventKindToolResult {
			continue
		}
		payload := string(ev.Payload)
		if strings.Contains(payload, "list_resources") && strings.Contains(payload, devID) && strings.Contains(payload, nullID) {
			sawListBoth = true
		}
		if strings.Contains(payload, "provision_resource") && strings.Contains(payload, "null_resource") &&
			!strings.Contains(payload, `"is_error":true`) && !strings.Contains(payload, `"is_error": true`) {
			sawProvisionOK = true
		}
	}
	if !sawListBoth {
		t.Fatal("list_resources tool_result did not include both resource ids")
	}
	if !sawProvisionOK {
		t.Fatal("provision_resource tool_result missing success for null_resource")
	}

	var loopNullID string
	listedNull, err := client.Resources.ListResources(ctx, connect.NewRequest(&corev1.ListResourcesRequest{
		SessionId: sid, Kind: string(uc.ResourceKindNullResource),
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range listedNull.Msg.GetResources() {
		if r.GetId() != nullID && r.GetState() == corev1.ResourceState_RESOURCE_STATE_READY {
			loopNullID = r.GetId()
		}
	}
	if loopNullID == "" {
		t.Fatal("loop provision_resource did not create a second ready null_resource")
	}

	if _, err := client.Resources.TerminateResource(ctx, connect.NewRequest(&corev1.TerminateResourceRequest{ResourceId: devID})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Resources.TerminateResource(ctx, connect.NewRequest(&corev1.TerminateResourceRequest{ResourceId: nullID})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Resources.TerminateResource(ctx, connect.NewRequest(&corev1.TerminateResourceRequest{ResourceId: loopNullID})); err != nil {
		t.Fatal(err)
	}
	awaitResourceState(t, client, devID, corev1.ResourceState_RESOURCE_STATE_TERMINATED, 2*time.Minute)
	awaitResourceState(t, client, nullID, corev1.ResourceState_RESOURCE_STATE_TERMINATED, 30*time.Second)
	awaitResourceState(t, client, loopNullID, corev1.ResourceState_RESOURCE_STATE_TERMINATED, 30*time.Second)

	// Dual leak check: docker has no leftover for dev_env; store has no active
	// resources for the session (covers both null_resource instances).
	docker, err := localdocker.New(localdocker.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = docker.Close() })
	deadline := time.Now().Add(30 * time.Second)
	for {
		owned, err := docker.ListOwned(ctx)
		if err != nil {
			t.Fatal(err)
		}
		leaked := false
		for _, o := range owned {
			if string(o.ResourceID) == devID {
				leaked = true
				break
			}
		}
		if !leaked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dev_env leaked docker resources after terminate: %v", owned)
		}
		time.Sleep(200 * time.Millisecond)
	}

	active, err := stack.Store.Org(stack.OrgA.ID).Resources().ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range active {
		if r.SessionID == uc.SessionID(sid) {
			t.Fatalf("session still has active resource after terminate: %+v", r)
		}
	}

	// Terminated events for both kinds.
	events, err = stack.Store.Org(stack.OrgA.ID).Events().Range(ctx, uc.SessionID(sid), 0, 400)
	if err != nil {
		t.Fatal(err)
	}
	var termDev, termNull bool
	for _, ev := range events {
		if ev.Kind != uc.EventKindResourceTerminated {
			continue
		}
		payload := string(ev.Payload)
		if strings.Contains(payload, devID) {
			termDev = true
		}
		if strings.Contains(payload, nullID) {
			termNull = true
		}
	}
	if !termDev || !termNull {
		t.Fatalf("missing terminated events (dev=%v null=%v)", termDev, termNull)
	}
}

func awaitResourceState(t *testing.T, client *testclient.Client, id string, want corev1.ResourceState, timeout time.Duration) *corev1.Resource {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last *corev1.Resource
	for time.Now().Before(deadline) {
		got, err := client.Resources.GetResource(context.Background(), connect.NewRequest(&corev1.GetResourceRequest{ResourceId: id}))
		if err == nil {
			last = got.Msg.GetResource()
			if last.GetState() == want {
				return last
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("resource %s did not reach %v (last=%v)", id, want, last)
	return nil
}
