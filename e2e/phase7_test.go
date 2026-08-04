package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/provider/localdocker"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/mcp"
	"github.com/aleksclark/ultracore/secrets"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/modelscript"
	"github.com/aleksclark/ultracore/testkit/testclient"
)

// repoRoot resolves the repository root from the e2e package directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// assertCIOwnsGate fails when required CI does not run a gate script. Tests
// that skip locally rely on CI to enforce their gate; if that job is ever
// removed, the skip must become a failure instead of silently passing.
func assertCIOwnsGate(t *testing.T, script string) {
	t.Helper()
	workflow, err := os.ReadFile(filepath.Join(repoRoot(t), ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatalf("cannot read required CI workflow: %v", err)
	}
	if !strings.Contains(string(workflow), script) {
		t.Fatalf("required CI no longer runs %s; this gate would be unenforced", script)
	}
}

// runScript runs a repository script and returns its combined output.
func runScript(t *testing.T, timeout time.Duration, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", append([]string{name}, args...)...)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// A7.1 — the generated-code drift gates fail for the intended reason under a
// deliberate proto/output mismatch, and the restored tree passes. The queue
// half of A7.1 is the shared conformance suite, which both river and inproc run
// in the unit job.
//
// The generator is not installed in every job, so this test can skip when buf
// is absent. A skip is only acceptable because another required job owns the
// gate, and that ownership is asserted here rather than assumed: a skip whose
// owning job has been deleted would otherwise look exactly like a pass.
func TestA71_CodegenDriftGate(t *testing.T) {
	assertCIOwnsGate(t, "scripts/mutate-codegen-gate.sh")
	if _, err := exec.LookPath("buf"); err != nil {
		t.Skip("buf unavailable; the required CI 'gates' job enforces this")
	}
	out, err := runScript(t, 30*time.Minute, "scripts/mutate-codegen-gate.sh")
	if err != nil {
		// The generator uses rate-limited remote plugins. A throttled run is a
		// tooling outage, not a drift verdict, so it must be reported as
		// neither a pass nor a drift failure.
		if strings.Contains(out, "buf generate could not run") {
			t.Skipf("code generator unavailable (rate limited); rerun when available:\n%s", out)
		}
		t.Fatalf("codegen drift mutation gate failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"detected as required (hand-edited generated output is visible)",
		"gate failed as required (Go/TS drift detected)",
		"restored tree passes the Go/TS codegen gate",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("mutation gate output missing %q:\n%s", want, out)
		}
	}
	// The gate restores the files it mutates. Other tree dirt from the
	// extraction worktree is unrelated.
	sessionPB, err := os.ReadFile(filepath.Join(repoRoot(t), "gen/go/core/v1/session.pb.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sessionPB), "deliberate drift injected by mutate-codegen-gate.sh") {
		t.Fatal("mutation gate left deliberate drift in session.pb.go")
	}
	protoBody, err := os.ReadFile(filepath.Join(repoRoot(t), "proto/core/v1/session.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(protoBody), "drift_probe") {
		t.Fatal("mutation gate left drift_probe in session.proto")
	}
}

// A7.2 — one scripted run emits multiple ordered text deltas that a subscriber
// observes strictly before the terminal event, and replay from seq 0 yields the
// same final timeline. 
func TestA72_IncrementalRendering(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, alice, string(stack.TenantA.ID), "incremental")
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{Text: "one two three four five six", ChunkSize: 3, ChunkDelay: 40 * time.Millisecond},
	}})

	sub, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	run, _, err := alice.StartRun(ctx, sess.GetId(), "stream to me")
	if err != nil {
		t.Fatal(err)
	}
	events := sub.CollectUntil(t, 60*time.Second, isTerminalRunEvent)

	// Deltas must be ordered, indexed, and strictly before the terminal event.
	var deltaIndexes []int32
	terminalAt := -1
	for i, ev := range events {
		switch payload := ev.GetPayload().GetPayload().(type) {
		case *corev1.EventPayload_TextDelta:
			if terminalAt >= 0 {
				t.Fatalf("text delta at %d arrived after the terminal event at %d", i, terminalAt)
			}
			deltaIndexes = append(deltaIndexes, payload.TextDelta.GetDeltaIndex())
		default:
			if isTerminalRunEvent(ev) && terminalAt < 0 {
				terminalAt = i
			}
		}
	}
	if len(deltaIndexes) < 2 {
		t.Fatalf("wanted at least 2 text deltas, got %d (%v)", len(deltaIndexes), kinds(events))
	}
	for i := 1; i < len(deltaIndexes); i++ {
		if deltaIndexes[i] <= deltaIndexes[i-1] {
			t.Fatalf("delta indexes not strictly increasing: %v", deltaIndexes)
		}
	}
	alice.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 30*time.Second)

	// Replay produces the same ordered timeline.
	replay, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	replayed := replay.CollectUntil(t, 60*time.Second, isTerminalRunEvent)
	if got, want := kinds(replayed), kinds(events); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("replayed timeline differs:\n got: %v\nwant: %v", got, want)
	}
}

// A7.3 — a planted credential is used successfully but appears nowhere: not in
// RPC payloads, event payloads, run history, database diagnostic columns,
// process logs, or error chains, in either literal or encoded form.
func TestA73_RedactionSweep(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, alice, string(stack.TenantA.ID), "redaction sweep")

	// The canary credential is the one the run actually authenticates with.
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{Text: "used the credential", ChunkSize: 4},
	}})
	run, _, err := alice.StartRun(ctx, sess.GetId(), "use the credential")
	if err != nil {
		t.Fatal(err)
	}
	alice.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 60*time.Second)

	// The vendor call really carried the canary, so the sweep is meaningful.
	usedCanary := false
	for _, req := range stack.Model.Requests() {
		if strings.Contains(req.Authorization, harness.CanaryAPIKey) {
			usedCanary = true
		}
	}
	if !usedCanary {
		t.Fatal("the run never authenticated with the canary credential; the sweep would be vacuous")
	}

	// Force an error path that a naive implementation would decorate with
	// credential material.
	if _, err := alice.Resources.ExecPreview(ctx, connect.NewRequest(&corev1.ExecPreviewRequest{
		ResourceId: "00000000-0000-0000-0000-000000000000", Command: "true",
	})); err == nil {
		t.Fatal("ExecPreview against a missing environment should fail")
	} else {
		assertNoCanary(t, "error chain", err.Error())
	}

	// Every observable surface: events, run history, database diagnostics,
	// process logs, and API payloads.
	sub, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	events := sub.CollectUntil(t, 60*time.Second, isTerminalRunEvent)
	for _, ev := range events {
		payload, err := json.Marshal(ev.GetPayload())
		if err != nil {
			t.Fatal(err)
		}
		assertNoCanary(t, "event payload "+testclient.Kind(ev), string(payload))
	}

	stored, err := stack.Store.Tenant(stack.TenantA.ID).Runs().Get(ctx, uc.RunID(run.GetId()))
	if err != nil {
		t.Fatal(err)
	}
	assertNoCanary(t, "persisted run history", string(stored.History))
	assertNoCanary(t, "persisted run failure message", stored.FailureMessage)

	creds, err := alice.Credentials.ListCredentials(ctx, connect.NewRequest(&corev1.ListCredentialsRequest{
		TenantId: string(stack.TenantA.ID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	credJSON, err := json.Marshal(creds.Msg)
	if err != nil {
		t.Fatal(err)
	}
	assertNoCanary(t, "ListCredentials response", string(credJSON))

	runs, err := alice.Agents.ListRuns(ctx, connect.NewRequest(&corev1.ListRunsRequest{SessionId: sess.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	runsJSON, err := json.Marshal(runs.Msg)
	if err != nil {
		t.Fatal(err)
	}
	assertNoCanary(t, "ListRuns response", string(runsJSON))

	// Database diagnostic fields for credentials and environments.
	dbCred, err := stack.Store.Tenant(stack.TenantA.ID).Credentials().Get(ctx, uc.CredentialKindOpenAI, "default")
	if err != nil {
		t.Fatal(err)
	}
	assertNoCanary(t, "credential ciphertext at rest", string(dbCred.EncPayload))
	storedResources, err := stack.Store.Tenant(stack.TenantA.ID).Resources().List(ctx, uc.SessionID(sess.GetId()))
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range storedResources {
		assertNoCanary(t, "env diagnostic fields", env.FailureMessage+string(env.Endpoint)+string(env.TokenEnc))
	}

	assertNoCanary(t, "cored and worker logs", stack.Logs())
}

// assertNoCanary fails when any encoded form of the canary appears in text.
func assertNoCanary(t *testing.T, where, text string) {
	t.Helper()
	for _, form := range secrets.Encodings(harness.CanaryAPIKey) {
		if form == "" {
			continue
		}
		if strings.Contains(text, form) {
			t.Fatalf("%s leaked the credential canary (form %q)", where, form)
		}
	}
}

// awaitEnvState polls until a resource reaches a state or the deadline.
// Name retained for call-site stability in phase7 durability tests.
func awaitEnvState(t *testing.T, client *testclient.Client, resourceID string, want corev1.ResourceState, timeout time.Duration) *corev1.Resource {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last *corev1.Resource
	for time.Now().Before(deadline) {
		got, err := client.Resources.GetResource(context.Background(), connect.NewRequest(&corev1.GetResourceRequest{ResourceId: resourceID}))
		if err == nil {
			last = got.Msg.GetResource()
			if last.GetState() == want {
				return last
			}
			if want != corev1.ResourceState_RESOURCE_STATE_FAILED && last.GetState() == corev1.ResourceState_RESOURCE_STATE_FAILED {
				t.Fatalf("resource failed while waiting for %v: %s", want, last.GetFailureMessage())
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("resource %s never reached %v within %s (last %v)", resourceID, want, timeout, last.GetState())
	return nil
}

// A7.4 — an environment's workspace survives cored and worker death, and
// restarting it rotates the bearer token: the new token works, the old token is
// rejected, and a client cached before rotation cannot keep using it.
func TestA74_EnvDurabilityAndRotation(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, alice, string(stack.TenantA.ID), "durability")

	envProto := provisionEnv(t, stack, sess.GetId())
	envID := envProto.GetId()

	// Write a file through the shipped API.
	if _, err := alice.Resources.ExecPreview(ctx, connect.NewRequest(&corev1.ExecPreviewRequest{
		ResourceId: envID, Command: "echo durable-content > /work/state.txt",
	})); err != nil {
		t.Fatal(err)
	}

	before, err := stack.Store.Tenant(stack.TenantA.ID).Resources().Get(ctx, uc.ResourceID(envID))
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := secrets.NewAESKeyring(stack.MasterKey)
	if err != nil {
		t.Fatal(err)
	}
	oldTokenBytes, err := keyring.Decrypt(before.TokenEnc)
	if err != nil {
		t.Fatal(err)
	}
	oldToken := string(oldTokenBytes)
	// A client created before the restart, held across it: the "cached MCP
	// client" a long-running worker would hold.
	cached := mcp.NewClient(string(before.Endpoint), oldToken)
	if err := cached.Initialize(ctx); err != nil {
		t.Fatalf("cached client could not initialize before rotation: %v", err)
	}

	// Kill the control plane entirely — both cored and the worker — and
	// bring it back. The environment is a separate process tree, so its
	// survival must not depend on either.
	stack.KillWorker()
	stack.KillUltrad()
	if _, err := alice.Resources.GetResource(ctx, connect.NewRequest(&corev1.GetResourceRequest{ResourceId: envID})); err == nil {
		t.Fatal("cored still answered after being killed; the restart proves nothing")
	}
	stack.StartUltrad()
	stack.StartWorker()

	// The environment is still reachable and still holds its file.
	read, err := alice.Resources.ExecPreview(ctx, connect.NewRequest(&corev1.ExecPreviewRequest{
		ResourceId: envID, Command: "cat /work/state.txt",
	}))
	if err != nil {
		t.Fatalf("environment unreachable after control-plane restart: %v", err)
	}
	if !strings.Contains(read.Msg.GetOutput(), "durable-content") {
		t.Fatalf("workspace did not survive: %q", read.Msg.GetOutput())
	}

	// Restart rotates the token and bumps the epoch.
	restarted, err := alice.Resources.RestartResource(ctx, connect.NewRequest(&corev1.RestartResourceRequest{ResourceId: envID}))
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Msg.GetEventSeq() == 0 {
		t.Fatal("RestartResource returned no event seq")
	}
	ready := awaitEnvState(t, alice, envID, corev1.ResourceState_RESOURCE_STATE_READY, 3*time.Minute)
	if int(ready.GetEpoch()) <= before.Epoch {
		t.Fatalf("epoch did not advance: %d → %d", before.Epoch, ready.GetEpoch())
	}

	after, err := stack.Store.Tenant(stack.TenantA.ID).Resources().Get(ctx, uc.ResourceID(envID))
	if err != nil {
		t.Fatal(err)
	}
	newTokenBytes, err := keyring.Decrypt(after.TokenEnc)
	if err != nil {
		t.Fatal(err)
	}
	newToken := string(newTokenBytes)
	if newToken == oldToken {
		t.Fatal("restart did not rotate the environment token")
	}

	// The rotated token works and the workspace is intact.
	if err := mcp.NewClient(string(after.Endpoint), newToken).Initialize(ctx); err != nil {
		t.Fatalf("rotated token rejected: %v", err)
	}
	survived, err := alice.Resources.ExecPreview(ctx, connect.NewRequest(&corev1.ExecPreviewRequest{
		ResourceId: envID, Command: "cat /work/state.txt",
	}))
	if err != nil || !strings.Contains(survived.Msg.GetOutput(), "durable-content") {
		t.Fatalf("workspace lost across restart: %q %v", survived.Msg.GetOutput(), err)
	}

	// The prior token is rejected.
	if err := mcp.NewClient(string(after.Endpoint), oldToken).Initialize(ctx); err == nil {
		t.Fatal("pre-rotation token still authenticates")
	}
	// And the pre-rotation cached client cannot keep working either, whether
	// because its endpoint is gone or its token is refused.
	if err := cached.Initialize(ctx); err == nil {
		t.Fatal("a client cached before rotation still authenticates")
	}
	if _, err := cached.Call(ctx, "bash", json.RawMessage(`{"command":"echo cached"}`)); err == nil {
		t.Fatal("a client cached before rotation can still call tools")
	}
}

// A7.5 — killing an environment mid-run produces a typed tool failure and a
// documented terminal outcome, reconciliation is not a busy loop, and repeated
// termination is idempotent with no leaked resources.
func TestA75_FailureAndReconciliation(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, alice, string(stack.TenantA.ID), "env failure")
	envProto := provisionEnv(t, stack, sess.GetId())
	envID := uc.ResourceID(envProto.GetId())

	provider, err := localdocker.New(localdocker.Config{Image: harness.BezalelImage})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = provider.Close() }()

	// A multi-step run: the environment dies between the first tool call and
	// the second, so the second call must fail in a typed way.
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{ToolCalls: []modelscript.ToolCallSpec{{Name: "bash", Args: map[string]any{"command": "echo first-step"}}}},
		{ToolCalls: []modelscript.ToolCallSpec{{Name: "bash", Args: map[string]any{"command": "echo second-step"}}}},
		{Text: "handled the environment failure"},
	}})

	sub, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	run, _, err := alice.StartRun(ctx, sess.GetId(), "work then lose the environment")
	if err != nil {
		t.Fatal(err)
	}
	// Wait for the first tool result, then destroy the environment.
	sub.CollectUntil(t, 90*time.Second, func(ev *corev1.SessionEvent) bool {
		result, ok := ev.GetPayload().GetPayload().(*corev1.EventPayload_ToolResult)
		return ok && strings.Contains(result.ToolResult.GetContent(), "first-step")
	})
	if err := provider.KillByResourceID(ctx, envID); err != nil {
		t.Fatal(err)
	}

	// The run reaches a documented terminal state rather than hanging.
	events := sub.CollectUntil(t, 3*time.Minute, isTerminalRunEvent)
	terminal := events[len(events)-1]
	switch testclient.Kind(terminal) {
	case "run_completed", "run_failed":
	default:
		t.Fatalf("run reached an undocumented terminal state %q", testclient.Kind(terminal))
	}

	// The second tool call must fail in a typed way: an error-flagged result
	// naming the lost environment, not a silent success and not a hang. A run
	// that terminated without ever reporting the failed call would leave the
	// model unable to react, so the flagged result is the contract.
	typedFailure := false
	for _, ev := range events {
		result, ok := ev.GetPayload().GetPayload().(*corev1.EventPayload_ToolResult)
		if !ok || !result.ToolResult.GetIsError() {
			continue
		}
		if strings.Contains(strings.ToLower(result.ToolResult.GetContent()), "unavailable") {
			typedFailure = true
		}
	}
	if !typedFailure {
		t.Fatalf("no typed tool failure was recorded after the environment died: %v", kinds(events))
	}

	// The environment is failed, with a structured reason, and ResourceFailed
	// precedes the run's terminal event.
	failed := awaitEnvState(t, alice, string(envID), corev1.ResourceState_RESOURCE_STATE_FAILED, 60*time.Second)
	if failed.GetFailureMessage() == "" {
		t.Fatal("ResourceFailed carried no structured reason")
	}
	all, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer all.Close()
	ordered := all.CollectUntil(t, 60*time.Second, func(ev *corev1.SessionEvent) bool {
		return testclient.Kind(ev) == "resource_failed"
	})
	if len(ordered) == 0 || testclient.Kind(ordered[len(ordered)-1]) != "resource_failed" {
		t.Fatal("no resource_failed event was recorded")
	}
	for _, ev := range ordered[:len(ordered)-1] {
		if isTerminalRunEvent(ev) && testclient.Kind(ev) != "run_completed" {
			// A run may legitimately complete before the reconciler notices,
			// but a failure must not precede the environment failure it is
			// attributed to.
			t.Fatalf("run terminated with %q before resource_failed", testclient.Kind(ev))
		}
	}
	alice.AwaitRunState(t, run.GetId(), stateOf(terminal), 30*time.Second)

	// Reconciliation must not busy loop: a failed environment stops being
	// rescheduled, so the queue drains.
	deadline := time.Now().Add(60 * time.Second)
	drained := false
	for time.Now().Before(deadline) {
		depth, err := stack.QueueDepth(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if depth == 0 {
			drained = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !drained {
		t.Fatal("queue never drained; reconciliation is busy looping on a failed environment")
	}

	// Repeated termination is idempotent and leaves no resources behind.
	for range 3 {
		if _, err := alice.Resources.TerminateResource(ctx, connect.NewRequest(&corev1.TerminateResourceRequest{ResourceId: string(envID)})); err != nil {
			t.Fatalf("repeated terminate failed: %v", err)
		}
	}
	leakDeadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(leakDeadline) {
		leaked, err := provider.Resources(ctx, envID)
		if err != nil {
			t.Fatal(err)
		}
		if len(leaked) == 0 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	leaked, _ := provider.Resources(ctx, envID)
	t.Fatalf("terminated environment leaked resources: %v", leaked)
}

func stateOf(ev *corev1.SessionEvent) corev1.RunState {
	switch testclient.Kind(ev) {
	case "run_completed":
		return corev1.RunState_RUN_STATE_COMPLETED
	case "run_failed":
		return corev1.RunState_RUN_STATE_FAILED
	default:
		return corev1.RunState_RUN_STATE_CANCELLED
	}
}

// A7.5 — provisioning interrupted by worker death converges without creating a
// duplicate resource.
func TestA75_InterruptedProvisioning(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, alice, string(stack.TenantA.ID), "interrupted provisioning")

	provider, err := localdocker.New(localdocker.Config{Image: harness.BezalelImage})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = provider.Close() }()

	// The interruption window is a race against provisioning, so a missed
	// window is retried with a fresh environment rather than skipped: a
	// skipped test is silently absent evidence.
	var envID uc.ResourceID
	killed := false
	for attempt := 1; attempt <= 5 && !killed; attempt++ {
		requested, err := alice.Resources.ProvisionResource(ctx, connect.NewRequest(&corev1.ProvisionResourceRequest{
			SessionId:        sess.GetId(),
			Spec:             &corev1.DevEnvSpec{Name: fmt.Sprintf("interrupted-%d", attempt), Workdir: "/work"},
			ProviderInstance: "default",
		}))
		if err != nil {
			t.Fatal(err)
		}
		envID = uc.ResourceID(requested.Msg.GetResource().GetId())

		// Kill the worker while it is provisioning, before readiness is
		// durable.
		killDeadline := time.Now().Add(60 * time.Second)
		missed := false
		for time.Now().Before(killDeadline) {
			current, err := stack.Store.Tenant(stack.TenantA.ID).Resources().Get(ctx, envID)
			if err != nil {
				t.Fatal(err)
			}
			if current.State == uc.ResourceProvisioning {
				stack.KillWorker()
				killed = true
				break
			}
			if current.State == uc.ResourceReady || current.State == uc.ResourceFailed {
				missed = true
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if killed {
			break
		}
		if !missed {
			t.Fatal("environment never entered provisioning")
		}
		// Clean up the environment that won the race and try again.
		if _, err := alice.Resources.TerminateResource(ctx, connect.NewRequest(&corev1.TerminateResourceRequest{
			ResourceId: string(envID),
		})); err != nil {
			t.Fatal(err)
		}
		t.Logf("attempt %d: environment settled before the interruption window; retrying", attempt)
	}
	if !killed {
		t.Fatal("never observed a provisioning window to interrupt after 5 attempts")
	}

	// A fresh worker must converge the environment, adopting whatever resource
	// the dead worker created rather than starting a second one.
	stack.StartWorker()
	ready := awaitEnvState(t, alice, string(envID), corev1.ResourceState_RESOURCE_STATE_READY, 4*time.Minute)
	if ready.GetEndpoint() == "" {
		t.Fatal("recovered environment has no endpoint")
	}

	resources, err := provider.Resources(ctx, envID)
	if err != nil {
		t.Fatal(err)
	}
	containers := 0
	for _, res := range resources {
		if strings.HasPrefix(res, "container:") {
			containers++
		}
	}
	if containers != 1 {
		t.Fatalf("interrupted provisioning converged with %d containers, want exactly 1: %v", containers, resources)
	}

	// The recovered environment is usable.
	out, err := alice.Resources.ExecPreview(ctx, connect.NewRequest(&corev1.ExecPreviewRequest{
		ResourceId: string(envID), Command: "echo recovered",
	}))
	if err != nil || !strings.Contains(out.Msg.GetOutput(), "recovered") {
		t.Fatalf("recovered environment unusable: %q %v", out.Msg.GetOutput(), err)
	}
}
// assertSameDenial requires cross-tenant and missing-resource errors to be
// indistinguishable, so no existence oracle leaks across orgs.
func assertSameDenial(t *testing.T, rpc string, crossTenant, missing error) {
	t.Helper()
	if crossTenant == nil {
		t.Fatalf("%s: cross-tenant access succeeded", rpc)
	}
	if missing == nil {
		t.Fatalf("%s: missing-resource access succeeded", rpc)
	}
	if connect.CodeOf(crossTenant) != connect.CodeOf(missing) {
		t.Fatalf("%s: cross-tenant code %v differs from missing code %v", rpc, connect.CodeOf(crossTenant), connect.CodeOf(missing))
	}
	if crossTenant.Error() != missing.Error() {
		t.Fatalf("%s: cross-tenant message %q differs from missing message %q", rpc, crossTenant.Error(), missing.Error())
	}
}

// A7.8 — the documented one-command stack boots, the noninteractive smoke
// exercises sessions, streaming, and environments, and teardown leaves no
// owned process or container.
func TestA78_DevStackSmoke(t *testing.T) {
	assertCIOwnsGate(t, "scripts/dev-stack.sh smoke")
	if os.Getenv("CI") == "" && os.Getenv("CORE_DEV_STACK_TESTS") == "" {
		t.Skip("set CORE_DEV_STACK_TESTS=1 to run the dev-stack smoke locally")
	}
	harness.EnsureBezalelImage(t)

	before := ownedContainers(t)
	out, err := runScript(t, 20*time.Minute, "scripts/dev-stack.sh", "smoke")
	if err != nil {
		t.Fatalf("dev stack smoke failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"seeded tenant",
		"streamed",
		"ready",
		"ran a command in the environment",
		"environment terminated",
		"smoke passed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("smoke output missing %q:\n%s", want, out)
		}
	}

	// No leaked environment containers and no leaked stack processes.
	after := ownedContainers(t)
	for id := range after {
		if !before[id] {
			t.Fatalf("dev stack smoke leaked environment container %s", id)
		}
	}
	for _, name := range []string{"ultra-smoke-"} {
		leaked, err := exec.Command("docker", "ps", "-aq", "--filter", "name="+name).Output()
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(leaked)) != "" {
			t.Fatalf("dev stack smoke leaked containers named %s*: %s", name, leaked)
		}
	}
	if pids, err := exec.Command("pgrep", "-f", "devstack model").Output(); err == nil && strings.TrimSpace(string(pids)) != "" {
		t.Fatalf("dev stack smoke leaked model processes: %s", pids)
	}
}

func ownedContainers(t *testing.T) map[string]bool {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-aq", "--filter", "label=ultracore.resource_id").Output()
	if err != nil {
		t.Fatal(err)
	}
	owned := map[string]bool{}
	for _, id := range strings.Fields(string(out)) {
		owned[id] = true
	}
	return owned
}

// A7.9 — coverage verification rejects nonexistent tests, tests absent from
// required CI, references whose tests do not assert the capability, a
// capability quietly deleted from the matrix, and a published RPC with no
// coverage and no explicit deferral. The unmutated tree passes.
func TestA79_EvidenceIntegrity(t *testing.T) {
	out, err := runScript(t, 5*time.Minute, "scripts/mutate-coverage-gate.sh")
	if err != nil {
		t.Fatalf("evidence integrity mutation gate failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"a reference to a nonexistent test file",
		"a test name the referenced file does not declare",
		"an assertion the referenced test does not contain",
		"evidence that required CI never executes",
		"a capability deleted from the matrix, leaving its RPCs unaccounted for",
		"a published RPC with neither coverage nor an explicit deferral",
		"the restored tree passes",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("mutation gate output missing %q:\n%s", want, out)
		}
	}

	// And the real matrix passes on the restored tree.
	verify := exec.Command("python3", "scripts/verify-coverage.py")
	verify.Dir = repoRoot(t)
	if verified, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("coverage verification failed on the restored tree: %v\n%s", err, verified)
	}
}

// A7.9 — "required CI" must be a fact about the repository, not a claim in a
// document. Every job the evidence gates depend on has to exist in the
// workflow and be listed as a required status check on the default branch;
// otherwise a red build can merge and every gate above becomes advisory.
func TestA79_RequiredChecksAreEnforced(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh unavailable; cannot query branch protection")
	}
	root := repoRoot(t)
	workflow, err := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	required := []string{"lint", "gates", "test", "dev-stack", "functional"}
	for _, job := range required {
		if !strings.Contains(string(workflow), "\n  "+job+":") {
			t.Fatalf("required CI job %q is missing from the workflow", job)
		}
	}

	out, err := exec.Command("gh", "api",
		"repos/aleksclark/ultracore/branches/master/protection/required_status_checks",
	).Output()
	if err != nil {
		t.Skipf("cannot read branch protection (needs repo admin token): %v", err)
	}
	var checks struct {
		Strict   bool     `json:"strict"`
		Contexts []string `json:"contexts"`
	}
	if err := json.Unmarshal(out, &checks); err != nil {
		t.Fatal(err)
	}
	if !checks.Strict {
		t.Fatal("required status checks are not strict; a stale branch can merge past the gates")
	}
	for _, job := range required {
		if !slices.Contains(checks.Contexts, job) {
			t.Fatalf("CI job %q is not a required status check: %v", job, checks.Contexts)
		}
	}
}
