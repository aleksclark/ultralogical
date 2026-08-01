package e2e

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/testclient"
)

// progressKeys returns an invocation's recorded progress keys in order.
func progressKeys(inv *ultrav1.FlowInvocation) []string {
	out := make([]string, 0, len(inv.GetProgress()))
	for _, entry := range inv.GetProgress() {
		out = append(out, entry.GetKey())
	}
	return out
}

// replayedFlowEvents collects an invocation's flow events by replaying the
// session log from seq 0, which is the only evidence that matters for a claim
// about reconstructable history.
func replayedFlowEvents(t *testing.T, c *testclient.Client, session, invocation string) (progress []string, terminal *ultrav1.FlowInvocationTerminal, invoked *ultrav1.FlowInvoked) {
	t.Helper()
	sub, err := c.Subscribe(context.Background(), session, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	events := sub.CollectUntil(t, 60*time.Second, func(ev *ultrav1.SessionEvent) bool {
		payload := ev.GetPayload().GetFlowInvocationTerminal()
		return payload != nil && payload.GetInvocationId() == invocation
	})
	var lastSeq int64
	for _, ev := range events {
		if ev.GetSeq() != lastSeq+1 {
			t.Fatalf("replayed log has a gap: seq %d follows %d", ev.GetSeq(), lastSeq)
		}
		lastSeq = ev.GetSeq()
		switch {
		case ev.GetPayload().GetFlowInvoked() != nil:
			if ev.GetPayload().GetFlowInvoked().GetInvocationId() == invocation {
				invoked = ev.GetPayload().GetFlowInvoked()
			}
		case ev.GetPayload().GetFlowInvocationProgressed() != nil:
			entry := ev.GetPayload().GetFlowInvocationProgressed()
			if entry.GetInvocationId() == invocation {
				progress = append(progress, entry.GetKey())
			}
		case ev.GetPayload().GetFlowInvocationTerminal() != nil:
			if ev.GetPayload().GetFlowInvocationTerminal().GetInvocationId() == invocation {
				terminal = ev.GetPayload().GetFlowInvocationTerminal()
				// The terminal transition is the last recorded progress step,
				// so replay reconstructs the same key sequence the invocation
				// persisted rather than one that stops a step short.
				progress = append(progress, "terminal")
			}
		}
	}
	return progress, terminal, invoked
}

// A9.2 — an invocation's provenance and rendering are frozen: publishing a
// later version cannot change what the earlier invocation did, live or on
// replay.
func TestA92_DeterministicProvenance(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	stack.Model.SetScript(flowScript())
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	v1 := putFlow(t, alice, org, "provenance", singleAgentFlow)
	session := createSession(t, alice, org, "provenance")
	invoked := invokeFlow(t, alice, session.GetId(), "provenance", `{"subject":"database"}`)
	invocationID := invoked.GetInvocationId()

	// Publishing a new version mid-flight must not touch the running one.
	second := strings.Replace(singleAgentFlow, "flow reviewer: {{.subject}}", "flow reviewer: REWRITTEN {{.subject}}", 1)
	v2 := putFlow(t, alice, org, "provenance", second)
	if v2.GetVersion() != 2 {
		t.Fatalf("v2 = %d", v2.GetVersion())
	}

	final := invocationTerminal(t, alice, invocationID, 90*time.Second)
	if final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED {
		t.Fatalf("state = %v reason %q message %q", final.GetState(), final.GetTerminalReason(), final.GetMessage())
	}
	if final.GetFlowId() != v1.GetId() || final.GetFlowVersion() != 1 {
		t.Fatalf("invocation drifted to %s v%d", final.GetFlowId(), final.GetFlowVersion())
	}
	if final.GetTerminalReason() != ultra.FlowTerminalCompleted {
		t.Fatalf("terminal reason = %q", final.GetTerminalReason())
	}

	// The rendered record persisted with the invocation is what executed.
	var rendered ultra.RenderedFlow
	if err := json.Unmarshal([]byte(final.GetRenderedJson()), &rendered); err != nil {
		t.Fatalf("rendered_json is not a RenderedFlow: %v", err)
	}
	agent, ok := rendered.FindAgent("reviewer")
	if !ok || agent.Prompt != "flow reviewer: database" {
		t.Fatalf("rendered prompt = %q", agent.Prompt)
	}
	if len(final.GetRuns()) != 1 {
		t.Fatalf("want 1 run, got %d", len(final.GetRuns()))
	}
	runView := final.GetRuns()[0]
	if runView.GetAgentName() != "reviewer" {
		t.Fatalf("run agent name = %q", runView.GetAgentName())
	}

	// The persisted run carries the same provenance and the same rendering.
	stored, err := stack.Store.Org(stack.OrgA.ID).Runs().Get(ctx, ultra.RunID(runView.GetRunId()))
	if err != nil {
		t.Fatal(err)
	}
	if stored.FlowInvocationID == nil || string(*stored.FlowInvocationID) != invocationID {
		t.Fatal("run lost its invocation provenance")
	}
	if stored.FlowAgentName != "reviewer" {
		t.Fatalf("run flow agent name = %q", stored.FlowAgentName)
	}
	if stored.Prompt != "flow reviewer: database" {
		t.Fatalf("persisted prompt = %q; a later version altered in-flight work", stored.Prompt)
	}

	// The run reachable through the public API agrees.
	apiRun, err := alice.Agents.GetRun(ctx, connect.NewRequest(&ultrav1.GetRunRequest{RunId: runView.GetRunId()}))
	if err != nil {
		t.Fatal(err)
	}
	if apiRun.Msg.GetRun().GetFlowInvocationId() != invocationID {
		t.Fatalf("API run provenance = %q", apiRun.Msg.GetRun().GetFlowInvocationId())
	}
	if apiRun.Msg.GetRun().GetFlowAgentName() != "reviewer" {
		t.Fatalf("API run agent name = %q", apiRun.Msg.GetRun().GetFlowAgentName())
	}

	// Replay from seq 0 rebuilds the same provenance and the same progress.
	replayProgress, terminal, invokedEvent := replayedFlowEvents(t, alice, session.GetId(), invocationID)
	if invokedEvent == nil {
		t.Fatal("replay is missing the flow_invoked event")
	}
	if invokedEvent.GetFlowId() != v1.GetId() || invokedEvent.GetFlowVersion() != 1 {
		t.Fatalf("replayed provenance = %s v%d", invokedEvent.GetFlowId(), invokedEvent.GetFlowVersion())
	}
	if terminal == nil || terminal.GetTerminalReason() != ultra.FlowTerminalCompleted {
		t.Fatalf("replayed terminal = %v", terminal)
	}
	if !equalStrings(replayProgress, progressKeys(final)) {
		t.Fatalf("replayed progress %v differs from persisted %v", replayProgress, progressKeys(final))
	}

	// Invoking again without a version pin uses the new version, proving the
	// pin is per-invocation rather than global.
	stack.Model.SetScript(flowScript())
	next := invokeFlow(t, alice, session.GetId(), "provenance", `{"subject":"cache"}`)
	nextFinal := invocationTerminal(t, alice, next.GetInvocationId(), 90*time.Second)
	if nextFinal.GetFlowVersion() != 2 {
		t.Fatalf("new invocation used version %d, want 2", nextFinal.GetFlowVersion())
	}
	if !strings.Contains(nextFinal.GetRenderedJson(), "REWRITTEN") {
		t.Fatal("new invocation did not use the new definition")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const twoEnvFlow = `{
  "params": {"subject": {"type": "string", "required": true}},
  "envs": {
    "alpha": {"provider_instance": "default", "workdir": "/work"},
    "beta": {"provider_instance": "default", "workdir": "/work"}
  },
  "agents": {
    "inspector": {"prompt": "flow env agent: {{.subject}}", "entry": true,
                  "envs": ["alpha"], "tools": ["bash", "view", "post_event"]},
    "reviewer": {"prompt": "flow reviewer: {{.subject}}", "entry": true,
                 "envs": ["beta"], "tools": ["post_event"]}
  }
}`

// A9.3 — no agent starts until every required environment is ready, and each
// agent receives only the environments it declared.
func TestA93_ReadinessGate(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	stack.Model.SetScript(flowScript())
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	putFlow(t, alice, org, "two-envs", twoEnvFlow)
	session := createSession(t, alice, org, "readiness")
	invoked := invokeFlow(t, alice, session.GetId(), "two-envs", `{"subject":"gate"}`)
	id := invoked.GetInvocationId()

	// Accepting an invocation must not start any run: the response itself is
	// evidence that agents are gated, not merely slow.
	if len(invoked.GetRunIds()) != 0 {
		t.Fatalf("InvokeFlow returned runs before readiness: %v", invoked.GetRunIds())
	}

	// While any environment is not ready, no run may exist. Sample repeatedly
	// during provisioning rather than once, so an early start cannot slip
	// through between polls.
	sawProvisioning := false
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		current := getInvocation(t, alice, id)
		ready := 0
		for _, env := range current.GetEnvs() {
			if env.GetState() == ultrav1.EnvState_ENV_STATE_READY {
				ready++
			}
		}
		if current.GetState() == ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_PROVISIONING {
			sawProvisioning = true
		}
		if ready < 2 && len(current.GetRuns()) > 0 {
			t.Fatalf("a run started with only %d/2 environments ready", ready)
		}
		if ready == 2 && len(current.GetRuns()) == 2 {
			break
		}
		if current.GetState() == ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_FAILED {
			t.Fatalf("invocation failed: %s %s", current.GetTerminalReason(), current.GetMessage())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !sawProvisioning {
		t.Fatal("the invocation never reported a provisioning stage; delayed readiness was not observable")
	}

	final := invocationTerminal(t, alice, id, 180*time.Second)
	if final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED {
		t.Fatalf("state = %v reason %q %q", final.GetState(), final.GetTerminalReason(), final.GetMessage())
	}
	if len(final.GetEnvs()) != 2 {
		t.Fatalf("want 2 environments, got %d", len(final.GetEnvs()))
	}
	byName := map[string]*ultrav1.FlowInvocationEnv{}
	for _, env := range final.GetEnvs() {
		byName[env.GetEnvName()] = env
	}
	if byName["alpha"] == nil || byName["beta"] == nil {
		t.Fatalf("environments are not named by their declarations: %v", byName)
	}

	// Each run's grants name exactly the environment its agent declared.
	for _, view := range final.GetRuns() {
		run, err := stack.Store.Org(stack.OrgA.ID).Runs().Get(ctx, ultra.RunID(view.GetRunId()))
		if err != nil {
			t.Fatal(err)
		}
		if run.Grants.EnvAll {
			t.Fatalf("agent %q received blanket environment authority", view.GetAgentName())
		}
		want := map[string]string{"inspector": "alpha", "reviewer": "beta"}[view.GetAgentName()]
		if want == "" {
			t.Fatalf("unexpected agent %q", view.GetAgentName())
		}
		if len(run.Grants.Envs) != 1 || run.Grants.Envs[0] != ultra.EnvID(byName[want].GetEnvId()) {
			t.Fatalf("agent %q granted %v, want only %s", view.GetAgentName(), run.Grants.Envs, want)
		}
		other := map[string]string{"alpha": "beta", "beta": "alpha"}[want]
		if run.Grants.AllowsEnv(ultra.EnvID(byName[other].GetEnvId())) {
			t.Fatalf("agent %q can reach undeclared environment %s", view.GetAgentName(), other)
		}
	}

	// The readiness of each environment is visible as ordered progress.
	keys := progressKeys(final)
	for _, want := range []string{"env_requested:alpha", "env_requested:beta", "env_ready:alpha", "env_ready:beta"} {
		if !containsString(keys, want) {
			t.Fatalf("progress %v is missing %s", keys, want)
		}
	}
	if indexOf(keys, "env_ready:alpha") > indexOf(keys, "stage_started:0") ||
		indexOf(keys, "env_ready:beta") > indexOf(keys, "stage_started:0") {
		t.Fatalf("agents started before readiness was recorded: %v", keys)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func indexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}

const topologyFlow = `{
  "params": {"topic": {"type": "string", "required": true}},
  "agents": {
    "planner": {"prompt": "flow reviewer: plan {{.topic}}", "entry": true, "tools": ["post_event"]},
    "worker_alpha": {"prompt": "flow worker alpha: {{.topic}}", "after": ["planner"], "tools": ["post_event"]},
    "worker_beta": {"prompt": "flow worker beta: {{.topic}}", "after": ["planner"], "tools": ["post_event"]},
    "summarizer": {"prompt": "flow summarizer: {{.topic}}", "after": ["worker_alpha", "worker_beta"], "tools": ["post_event"]}
  }
}`

// A9.4 — declared topology executes in order, with parent links, stable cohort
// ordinals, and a reproducible terminal result.
func TestA94_Topology(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	stack.Model.SetScript(flowScript())
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	putFlow(t, alice, org, "topology", topologyFlow)
	session := createSession(t, alice, org, "topology")
	invoked := invokeFlow(t, alice, session.GetId(), "topology", `{"topic":"latency"}`)
	id := invoked.GetInvocationId()

	// A later stage may not start before every run of an earlier stage is
	// terminal. Sample during execution: checking only the final state would
	// pass even if everything started at once.
	seenStages := map[string]bool{}
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		current := getInvocation(t, alice, id)
		byName := map[string]*ultrav1.FlowInvocationRun{}
		for _, run := range current.GetRuns() {
			byName[run.GetAgentName()] = run
			seenStages[run.GetAgentName()] = true
		}
		if byName["worker_alpha"] != nil && byName["planner"].GetState() != ultrav1.RunState_RUN_STATE_COMPLETED {
			t.Fatalf("worker started while planner was %v", byName["planner"].GetState())
		}
		if byName["summarizer"] != nil {
			for _, worker := range []string{"worker_alpha", "worker_beta"} {
				if byName[worker].GetState() != ultrav1.RunState_RUN_STATE_COMPLETED {
					t.Fatalf("summarizer started while %s was %v", worker, byName[worker].GetState())
				}
			}
		}
		if current.GetState() == ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED {
			break
		}
		if current.GetState() == ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_FAILED {
			t.Fatalf("invocation failed: %s %s", current.GetTerminalReason(), current.GetMessage())
		}
		time.Sleep(50 * time.Millisecond)
	}

	final := invocationTerminal(t, alice, id, 120*time.Second)
	if final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED {
		t.Fatalf("state = %v reason %q", final.GetState(), final.GetTerminalReason())
	}
	if len(final.GetRuns()) != 4 {
		t.Fatalf("want 4 runs, got %d", len(final.GetRuns()))
	}
	byName := map[string]*ultrav1.FlowInvocationRun{}
	for _, run := range final.GetRuns() {
		byName[run.GetAgentName()] = run
	}
	for _, name := range []string{"planner", "worker_alpha", "worker_beta", "summarizer"} {
		if byName[name] == nil {
			t.Fatalf("agent %q never ran", name)
		}
		if byName[name].GetState() != ultrav1.RunState_RUN_STATE_COMPLETED {
			t.Fatalf("agent %q finished %v", name, byName[name].GetState())
		}
	}
	// A dependent agent is parented by a declared dependency, so a rendered
	// run tree shows the topology rather than four unrelated roots.
	if byName["worker_alpha"].GetParentRunId() != byName["planner"].GetRunId() {
		t.Fatal("worker_alpha is not parented by planner")
	}
	if byName["summarizer"].GetParentRunId() != byName["worker_alpha"].GetRunId() {
		t.Fatal("summarizer is not parented by its first declared dependency")
	}
	// Agents sharing a stage share one cohort with stable ordinals.
	if byName["worker_alpha"].GetCohortId() == "" ||
		byName["worker_alpha"].GetCohortId() != byName["worker_beta"].GetCohortId() {
		t.Fatalf("stage cohort ids are not shared: %q vs %q",
			byName["worker_alpha"].GetCohortId(), byName["worker_beta"].GetCohortId())
	}
	if byName["worker_alpha"].GetCohortOrdinal() == byName["worker_beta"].GetCohortOrdinal() {
		t.Fatal("cohort ordinals collide")
	}
	if byName["planner"].GetCohortId() != "" {
		t.Fatal("a single-agent stage should not form a cohort")
	}
	for _, run := range final.GetRuns() {
		stored, err := stack.Store.Org(stack.OrgA.ID).Runs().Get(ctx, ultra.RunID(run.GetRunId()))
		if err != nil {
			t.Fatal(err)
		}
		if stored.FlowInvocationID == nil || string(*stored.FlowInvocationID) != id {
			t.Fatalf("run %s lost its invocation link", run.GetRunId())
		}
	}
	keys := progressKeys(final)
	for stage := range 3 {
		start := "stage_started:" + strconv.Itoa(stage)
		done := "stage_complete:" + strconv.Itoa(stage)
		if indexOf(keys, start) < 0 || indexOf(keys, done) < 0 {
			t.Fatalf("progress %v is missing stage %d", keys, stage)
		}
		if indexOf(keys, start) > indexOf(keys, done) {
			t.Fatalf("stage %d completed before it started: %v", stage, keys)
		}
	}
}

const catalogFlow = `{
  "agents": {
    "supervisor": {"prompt": "catalog supervisor: delegate", "entry": true,
                   "tools": ["spawn_agent", "post_event"], "may_spawn": true, "max_children": 4},
    "helper": {"prompt": "catalog helper: assist", "spawnable": true, "tools": ["post_event"]}
  }
}`

const forbiddenCatalogFlow = `{
  "agents": {
    "supervisor": {"prompt": "forbidden supervisor: delegate", "entry": true,
                   "tools": ["spawn_agent", "post_event"], "may_spawn": true, "max_children": 4},
    "not_spawnable": {"prompt": "never started: should not run", "after": ["supervisor"], "tools": ["post_event"]}
  }
}`

// A9.4 — a running agent can launch a spawnable catalog agent by name, and an
// agent the flow did not publish is refused uniformly.
func TestA94_AgentRefSpawn(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	stack.Model.SetScript(flowScript())
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	putFlow(t, alice, org, "catalog", catalogFlow)
	session := createSession(t, alice, org, "catalog")
	invoked := invokeFlow(t, alice, session.GetId(), "catalog", `{}`)
	final := invocationTerminal(t, alice, invoked.GetInvocationId(), 120*time.Second)
	if final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED {
		t.Fatalf("state = %v reason %q %q", final.GetState(), final.GetTerminalReason(), final.GetMessage())
	}

	// The spawned child must have the catalog's prompt, not whatever the model
	// supplied, and must inherit the invocation.
	runs, err := stack.Store.Org(stack.OrgA.ID).Flows().InvocationRuns(ctx,
		ultra.FlowInvocationID(invoked.GetInvocationId()))
	if err != nil {
		t.Fatal(err)
	}
	var spawned *ultra.AgentRun
	for i := range runs {
		if runs[i].FlowAgentName == "" {
			spawned = &runs[i]
		}
	}
	if spawned == nil {
		t.Fatalf("agent_ref spawned no child; runs = %d", len(runs))
	}
	if spawned.Prompt != "catalog helper: assist" {
		t.Fatalf("catalog child prompt = %q; the definition must supply it", spawned.Prompt)
	}
	if spawned.FlowInvocationID == nil || string(*spawned.FlowInvocationID) != invoked.GetInvocationId() {
		t.Fatal("catalog child lost its invocation provenance")
	}

	// An agent that exists but is not spawnable is refused, and the refusal is
	// recorded as a permission denial rather than a distinguishing error.
	putFlow(t, alice, org, "forbidden", forbiddenCatalogFlow)
	forbiddenSession := createSession(t, alice, org, "forbidden")
	forbidden := invokeFlow(t, alice, forbiddenSession.GetId(), "forbidden", `{}`)
	forbiddenFinal := invocationTerminal(t, alice, forbidden.GetInvocationId(), 120*time.Second)
	forbiddenRuns, err := stack.Store.Org(stack.OrgA.ID).Flows().InvocationRuns(ctx,
		ultra.FlowInvocationID(forbidden.GetInvocationId()))
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range forbiddenRuns {
		if run.FlowAgentName == "" {
			t.Fatal("a non-spawnable agent was launched through agent_ref")
		}
	}
	denied := false
	for _, event := range sessionEvents(t, alice, forbiddenSession.GetId(), forbidden.GetInvocationId()) {
		if payload := event.GetPayload().GetPermissionDenied(); payload != nil && payload.GetTool() == "spawn_agent" {
			denied = true
			if strings.Contains(payload.GetReason(), "not_spawnable") {
				t.Fatalf("denial reason leaks the referenced name: %q", payload.GetReason())
			}
		}
	}
	if !denied {
		t.Fatal("a forbidden agent_ref produced no permission denial")
	}
	_ = forbiddenFinal
}

// sessionEvents replays a session's log up to and including the terminal event
// of one invocation, so the collection is bounded by an observable condition
// rather than by a timeout.
func sessionEvents(t *testing.T, c *testclient.Client, session, invocation string) []*ultrav1.SessionEvent {
	t.Helper()
	sub, err := c.Subscribe(context.Background(), session, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	return sub.CollectUntil(t, 60*time.Second, func(ev *ultrav1.SessionEvent) bool {
		payload := ev.GetPayload().GetFlowInvocationTerminal()
		return payload != nil && payload.GetInvocationId() == invocation
	})
}
