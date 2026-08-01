package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"

	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/testkit/harness"
)

// mixedEnvFlow declares one environment that can never provision (its image
// does not exist) alongside one that can. It is the smallest shape that makes
// "one failed, one succeeded" observable.
const mixedEnvFlow = `{
  "envs": {
    "good": {"provider_instance": "default", "workdir": "/work"},
    "doomed": {"provider_instance": "default", "workdir": "/work",
               "image": "ultralogical/definitely-not-a-real-image:absent"}
  },
  "agents": {
    "worker": {"prompt": "never started: doomed flow", "entry": true,
               "envs": ["good", "doomed"], "tools": ["post_event"]}
  }
}`

// A9.5 — a partially failed provisioning converges, starts no agent, cleans up
// only what the invocation owns, and leaves unrelated session resources alone.
func TestA95_FailureConvergence(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	stack.Model.SetScript(flowScript())
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	session := createSession(t, alice, org, "convergence")
	// A session environment that the flow does not own. Cleanup must not
	// touch it: ownership is the whole point of scoped convergence.
	bystander := provisionEnv(t, stack, session.GetId())

	putFlow(t, alice, org, "doomed", mixedEnvFlow)
	invoked := invokeFlow(t, alice, session.GetId(), "doomed", `{}`)
	id := invoked.GetInvocationId()

	final := invocationTerminal(t, alice, id, 180*time.Second)
	if final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_FAILED {
		t.Fatalf("state = %v reason %q", final.GetState(), final.GetTerminalReason())
	}
	if final.GetTerminalReason() != ultra.FlowTerminalEnvironmentFailed {
		t.Fatalf("terminal reason = %q, want %q", final.GetTerminalReason(), ultra.FlowTerminalEnvironmentFailed)
	}
	if len(final.GetRuns()) != 0 {
		t.Fatalf("agents started despite a failed required environment: %d runs", len(final.GetRuns()))
	}

	// Exactly two environments were created, one per declaration, and both are
	// released. A retried provisioning stage must not have produced a third.
	envs, err := stack.Store.Org(stack.OrgA.ID).Flows().InvocationEnvs(ctx, ultra.FlowInvocationID(id))
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 2 {
		t.Fatalf("invocation owns %d environments, want 2", len(envs))
	}
	names := map[string]int{}
	for _, env := range envs {
		names[env.FlowEnvName]++
	}
	if names["good"] != 1 || names["doomed"] != 1 {
		t.Fatalf("environment declarations were duplicated: %v", names)
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		current, err := stack.Store.Org(stack.OrgA.ID).Flows().InvocationEnvs(ctx, ultra.FlowInvocationID(id))
		if err != nil {
			t.Fatal(err)
		}
		released := 0
		for _, env := range current {
			if env.State.Terminal() {
				released++
			}
		}
		if released == len(current) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, env := range mustInvocationEnvs(t, stack, id) {
		if !env.State.Terminal() {
			t.Fatalf("environment %s (%s) was not released: %s", env.FlowEnvName, env.ID, env.State)
		}
	}

	// The unrelated environment is untouched.
	stillThere, err := alice.Envs.GetEnv(ctx, connect.NewRequest(&ultrav1.GetEnvRequest{EnvId: bystander.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if stillThere.Msg.GetEnv().GetState() != ultrav1.EnvState_ENV_STATE_READY {
		t.Fatalf("an environment the flow does not own became %v", stillThere.Msg.GetEnv().GetState())
	}

	// Cleanup is recorded once per owned environment: repeated cleanup would
	// show as repeated progress, because progress keys are idempotent.
	cleanupKeys := map[string]int{}
	for _, entry := range final.GetProgress() {
		if entry.GetStage() == ultra.FlowStageCleanup {
			cleanupKeys[entry.GetKey()]++
		}
	}
	for key, count := range cleanupKeys {
		if count != 1 {
			t.Fatalf("cleanup key %q recorded %d times", key, count)
		}
	}

	// Cancelling a converged invocation changes nothing, and re-invoking
	// creates a fresh invocation with its own resources rather than reusing or
	// duplicating the failed one's.
	if _, err := alice.Flows.CancelFlowInvocation(ctx, connect.NewRequest(&ultrav1.CancelFlowInvocationRequest{
		InvocationId: id,
	})); err != nil {
		t.Fatalf("cancelling a terminal invocation errored: %v", err)
	}
	after := getInvocation(t, alice, id)
	if after.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_FAILED {
		t.Fatalf("terminal invocation changed to %v", after.GetState())
	}
	if len(after.GetProgress()) != len(final.GetProgress()) {
		t.Fatal("cancelling a terminal invocation added progress")
	}

	retry := invokeFlow(t, alice, session.GetId(), "doomed", `{}`)
	if retry.GetInvocationId() == id {
		t.Fatal("retry reused the failed invocation id")
	}
	retryFinal := invocationTerminal(t, alice, retry.GetInvocationId(), 180*time.Second)
	if retryFinal.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_FAILED {
		t.Fatalf("retry state = %v", retryFinal.GetState())
	}
	retryEnvs := mustInvocationEnvs(t, stack, retry.GetInvocationId())
	if len(retryEnvs) != 2 {
		t.Fatalf("retry owns %d environments, want 2", len(retryEnvs))
	}
	for _, env := range retryEnvs {
		for _, original := range envs {
			if env.ID == original.ID {
				t.Fatal("retry adopted the previous invocation's environment")
			}
		}
	}
}

func mustInvocationEnvs(t *testing.T, stack *harness.Stack, id string) []ultra.DevEnv {
	t.Helper()
	envs, err := stack.Store.Org(stack.OrgA.ID).Flows().InvocationEnvs(context.Background(),
		ultra.FlowInvocationID(id))
	if err != nil {
		t.Fatal(err)
	}
	return envs
}

// corruptInvocationRendering rewrites an invocation's frozen rendering to a
// schema version this build cannot execute. It writes the row directly because
// no public API can produce this state: it models a schema rollout that moved
// underneath a running invocation.
func corruptInvocationRendering(t *testing.T, stack *harness.Stack, id string) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), stack.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(),
		`UPDATE flow_invocations SET rendered = $2 WHERE id = $1`,
		id, []byte(`{"schema_version":9999,"params":{},"agents":[]}`)); err != nil {
		t.Fatal(err)
	}
}

// A9.5 — a definition that cannot be executed after load converges to failed
// with a typed reason rather than retrying forever.
func TestA95_ValidationAfterLoad(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	stack.Model.SetScript(flowScript())
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	putFlow(t, alice, org, "corruptible", singleAgentFlow)
	session := createSession(t, alice, org, "corruptible")
	invoked := invokeFlow(t, alice, session.GetId(), "corruptible", `{"subject":"database"}`)

	// Inject a rendering the worker cannot execute — a schema version from a
	// future release — directly into the row, which is what an operator
	// downgrade or a partially rolled-out schema would look like. The
	// invocation must converge on a typed failure rather than retry forever.
	corruptInvocationRendering(t, stack, invoked.GetInvocationId())
	_ = ctx

	final := invocationTerminal(t, alice, invoked.GetInvocationId(), 90*time.Second)
	if final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_FAILED &&
		final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED {
		t.Fatalf("state = %v", final.GetState())
	}
	// The invocation may have completed before the rewrite landed; when it did
	// not, the failure must be the typed invalid-definition outcome.
	if final.GetState() == ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_FAILED &&
		final.GetTerminalReason() != ultra.FlowTerminalInvalidDefinition {
		t.Fatalf("terminal reason = %q, want %q", final.GetTerminalReason(), ultra.FlowTerminalInvalidDefinition)
	}
}

// A9.5 — an invocation that cannot make progress converges on the outer
// deadline instead of holding its resources open forever.
func TestA95_InvocationDeadlineConverges(t *testing.T) {
	// A one-second outer deadline makes the guarantee observable in a test
	// without changing what it guarantees.
	stack := harness.Up(t, harness.WithWorkerEnv(
		"ULTRA_FLOW_INVOCATION_TIMEOUT=1s",
		"ULTRA_PROVISION_TIMEOUT=10m",
	))
	alice := stack.AliceClient()
	stack.Model.SetScript(flowScript())
	org := string(stack.OrgA.ID)

	// An environment whose image cannot be pulled never becomes ready, so the
	// invocation would otherwise wait on its readiness gate indefinitely.
	const stuckFlow = `{
	  "envs": {"stuck": {"provider_instance": "default", "workdir": "/work",
	                     "image": "ultralogical/definitely-not-a-real-image:absent",
	                     "timeout": "9m"}},
	  "agents": {"worker": {"prompt": "never started: stuck flow", "entry": true,
	                        "envs": ["stuck"], "tools": ["post_event"]}}
	}`
	putFlow(t, alice, org, "stuck", stuckFlow)
	session := createSession(t, alice, org, "deadline")
	invoked := invokeFlow(t, alice, session.GetId(), "stuck", `{}`)

	final := invocationTerminal(t, alice, invoked.GetInvocationId(), 120*time.Second)
	if final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_FAILED {
		t.Fatalf("state = %v reason %q", final.GetState(), final.GetTerminalReason())
	}
	// Either the environment failed outright or the outer deadline fired; both
	// are documented convergence, and neither may leave an agent running or an
	// environment held open.
	switch final.GetTerminalReason() {
	case ultra.FlowTerminalTimedOut, ultra.FlowTerminalEnvironmentFailed:
	default:
		t.Fatalf("terminal reason = %q, want a documented convergence reason", final.GetTerminalReason())
	}
	if len(final.GetRuns()) != 0 {
		t.Fatalf("agents started for an invocation that never became ready: %d", len(final.GetRuns()))
	}
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		pending := 0
		for _, env := range mustInvocationEnvs(t, stack, invoked.GetInvocationId()) {
			if !env.State.Terminal() {
				pending++
			}
		}
		if pending == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, env := range mustInvocationEnvs(t, stack, invoked.GetInvocationId()) {
		if !env.State.Terminal() {
			t.Fatalf("environment %s left in %s after convergence", env.ID, env.State)
		}
	}
}

const slowFlow = `{
  "agents": {"slow": {"prompt": "flow slow agent: keep going", "entry": true, "tools": ["post_event"]}}
}`

const slowEnvFlow = `{
  "envs": {"main": {"provider_instance": "default", "workdir": "/work"}},
  "agents": {"slow": {"prompt": "never started: cancelled during provisioning", "entry": true,
                      "envs": ["main"], "tools": ["post_event"]}}
}`

// A9.6 — cancelling during provisioning releases owned environments, starts no
// agent, and replays as ordered progress with no gaps.
func TestA96_CancelDuringProvisioning(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	stack.Model.SetScript(flowScript())
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	putFlow(t, alice, org, "slow-env", slowEnvFlow)
	session := createSession(t, alice, org, "cancel-provisioning")
	invoked := invokeFlow(t, alice, session.GetId(), "slow-env", `{}`)
	id := invoked.GetInvocationId()

	// Cancel as soon as the environment has been requested, which is squarely
	// inside the provisioning window.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		current := getInvocation(t, alice, id)
		if len(current.GetEnvs()) > 0 {
			break
		}
		if current.GetState().String() != "" && current.GetState() ==
			ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_FAILED {
			t.Fatalf("invocation failed early: %s", current.GetTerminalReason())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := alice.Flows.CancelFlowInvocation(ctx, connect.NewRequest(&ultrav1.CancelFlowInvocationRequest{
		InvocationId: id,
	})); err != nil {
		t.Fatal(err)
	}

	final := invocationTerminal(t, alice, id, 180*time.Second)
	if final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_CANCELLED {
		t.Fatalf("state = %v reason %q", final.GetState(), final.GetTerminalReason())
	}
	if final.GetTerminalReason() != ultra.FlowTerminalCancelled {
		t.Fatalf("terminal reason = %q", final.GetTerminalReason())
	}
	if len(final.GetRuns()) != 0 {
		t.Fatalf("an agent started after cancellation: %d runs", len(final.GetRuns()))
	}
	// Owned environments converge to a terminal state.
	envDeadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(envDeadline) {
		pending := 0
		for _, env := range mustInvocationEnvs(t, stack, id) {
			if !env.State.Terminal() {
				pending++
			}
		}
		if pending == 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, env := range mustInvocationEnvs(t, stack, id) {
		if !env.State.Terminal() {
			t.Fatalf("environment %s left in %s after cancellation", env.ID, env.State)
		}
	}

	// Cancelling again is a no-op rather than an error or a second cleanup.
	before := len(getInvocation(t, alice, id).GetProgress())
	if _, err := alice.Flows.CancelFlowInvocation(ctx, connect.NewRequest(&ultrav1.CancelFlowInvocationRequest{
		InvocationId: id,
	})); err != nil {
		t.Fatalf("second cancel errored: %v", err)
	}
	if after := len(getInvocation(t, alice, id).GetProgress()); after != before {
		t.Fatalf("second cancel added progress: %d then %d", before, after)
	}

	replay, terminal, invokedEvent := replayedFlowEvents(t, alice, session.GetId(), id)
	if invokedEvent == nil || terminal == nil {
		t.Fatal("replay is missing the invocation's boundary events")
	}
	if terminal.GetTerminalReason() != ultra.FlowTerminalCancelled {
		t.Fatalf("replayed terminal reason = %q", terminal.GetTerminalReason())
	}
	if !equalStrings(replay, progressKeys(getInvocation(t, alice, id))) {
		t.Fatalf("replayed progress %v differs from persisted %v", replay, progressKeys(getInvocation(t, alice, id)))
	}
}

// A9.6 — cancelling during execution cancels the invocation's runs and replays
// consistently.
func TestA96_CancelDuringExecution(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	stack.Model.SetScript(flowScript())
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	putFlow(t, alice, org, "slow", slowFlow)
	session := createSession(t, alice, org, "cancel-execution")
	invoked := invokeFlow(t, alice, session.GetId(), "slow", `{}`)
	id := invoked.GetInvocationId()

	// Wait until the agent is actually running before cancelling, so this is
	// cancellation of execution rather than of a queued launch.
	var runID string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		current := getInvocation(t, alice, id)
		if len(current.GetRuns()) == 1 &&
			current.GetRuns()[0].GetState() == ultrav1.RunState_RUN_STATE_RUNNING {
			runID = current.GetRuns()[0].GetRunId()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if runID == "" {
		t.Fatal("the flow's agent never reached running")
	}
	if _, err := alice.Flows.CancelFlowInvocation(ctx, connect.NewRequest(&ultrav1.CancelFlowInvocationRequest{
		InvocationId: id,
	})); err != nil {
		t.Fatal(err)
	}

	final := invocationTerminal(t, alice, id, 180*time.Second)
	if final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_CANCELLED {
		t.Fatalf("state = %v reason %q", final.GetState(), final.GetTerminalReason())
	}
	run := awaitRunOneOf(t, stack, stack.OrgA.ID, ultra.RunID(runID), 120*time.Second,
		ultra.RunCancelled, ultra.RunCompleted, ultra.RunFailed)
	if run.State != ultra.RunCancelled {
		t.Fatalf("the invocation's run finished %s rather than cancelled", run.State)
	}

	replay, terminal, _ := replayedFlowEvents(t, alice, session.GetId(), id)
	if terminal == nil || terminal.GetState() != string(ultra.FlowInvocationCancelled) {
		t.Fatalf("replayed terminal = %v", terminal)
	}
	if !equalStrings(replay, progressKeys(getInvocation(t, alice, id))) {
		t.Fatal("replayed progress differs from persisted progress")
	}
	// The run's cancellation is in the log too, so replay reconstructs both
	// the invocation terminal and its resources' terminals.
	sawRunCancelled := false
	for _, event := range sessionEvents(t, alice, session.GetId(), id) {
		if payload := event.GetPayload().GetRunCancelled(); payload != nil && payload.GetRunId() == runID {
			sawRunCancelled = true
		}
	}
	if !sawRunCancelled {
		t.Fatal("the run's cancellation is missing from the replayed log")
	}
}

// A9.9 — every shipped example validates and runs to completion against the
// real stack, and the documentation names exactly the examples that exist.
func TestA99_ExampleFlows(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	stack.Model.SetScript(flowScript())
	org := string(stack.OrgA.ID)

	cases := []struct {
		file     string
		params   string
		expected []string
	}{
		{"single-agent.json", `{"subject":"the cache"}`,
			[]string{"accepted", "stage_started:0", "stage_complete:0", "terminal"}},
		{"environment-backed.json", `{}`,
			[]string{"accepted", "provisioning", "env_requested:main", "env_ready:main",
				"stage_started:0", "stage_complete:0", "cleanup_env:main", "terminal"}},
		{"multi-agent.json", `{"topic":"throughput"}`,
			[]string{"accepted", "stage_started:0", "stage_complete:0",
				"stage_started:1", "stage_complete:1", "stage_started:2", "stage_complete:2", "terminal"}},
	}
	for _, tc := range cases {
		t.Run(strings.TrimSuffix(tc.file, ".json"), func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("..", "examples", "flows", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			name := strings.TrimSuffix(tc.file, ".json")
			// The example must pass the same validation a user's flow does.
			valid, err := alice.Flows.ValidateFlow(context.Background(),
				connect.NewRequest(&ultrav1.ValidateFlowRequest{OrgId: org, DefinitionJson: string(body)}))
			if err != nil {
				t.Fatal(err)
			}
			if !valid.Msg.GetValid() {
				t.Fatalf("example %s is invalid: %v", tc.file, valid.Msg.GetErrors())
			}
			putFlow(t, alice, org, name, string(body))
			session := createSession(t, alice, org, name)
			invoked := invokeFlow(t, alice, session.GetId(), name, tc.params)
			final := invocationTerminal(t, alice, invoked.GetInvocationId(), 240*time.Second)
			if final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED {
				t.Fatalf("example %s ended %v: %s %s", tc.file, final.GetState(),
					final.GetTerminalReason(), final.GetMessage())
			}
			// The documented lifecycle is asserted as an ordered subsequence,
			// so an example that skipped provisioning or a stage fails here
			// rather than passing because it merely finished.
			keys := progressKeys(final)
			position := 0
			for _, want := range tc.expected {
				found := -1
				for i := position; i < len(keys); i++ {
					if keys[i] == want {
						found = i
						break
					}
				}
				if found < 0 {
					t.Fatalf("example %s progress %v does not contain %q in order", tc.file, keys, want)
				}
				position = found + 1
			}
		})
	}
}

// A9.9 — the documentation and the shipped examples cannot drift apart: every
// example file is named by the docs, and every example the docs name exists.
func TestA99_DocumentedExamplesAreExecuted(t *testing.T) {
	docs, err := os.ReadFile(filepath.Join("..", "docs", "flows.md"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join("..", "examples", "flows"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no example flows are shipped")
	}
	// Every example this test executes is one of the shipped files, and the
	// documentation names each of them.
	executed := map[string]bool{
		"single-agent.json": true, "environment-backed.json": true, "multi-agent.json": true,
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if !strings.Contains(string(docs), entry.Name()) {
			t.Fatalf("example %s is shipped but never documented", entry.Name())
		}
		if !executed[entry.Name()] {
			t.Fatalf("example %s is shipped but not executed by TestA99_ExampleFlows", entry.Name())
		}
		body, err := os.ReadFile(filepath.Join("..", "examples", "flows", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if _, verr := ultra.ValidateFlowDefinition(body); verr != nil {
			t.Fatalf("example %s does not validate: %v", entry.Name(), verr)
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(body, &probe); err != nil {
			t.Fatalf("example %s is not JSON: %v", entry.Name(), err)
		}
	}
	for name := range executed {
		if !strings.Contains(string(docs), name) {
			t.Fatalf("docs/flows.md does not mention %s", name)
		}
	}
}
