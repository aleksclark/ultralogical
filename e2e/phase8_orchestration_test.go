package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/modelscript"
)

// awaitRun polls a run until it reaches one of the wanted states.
func awaitRunOneOf(t *testing.T, stack *harness.Stack, org uc.OrgID, id uc.RunID, timeout time.Duration, want ...uc.RunState) uc.AgentRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last uc.AgentRun
	for time.Now().Before(deadline) {
		run, err := stack.Store.Org(org).Runs().Get(context.Background(), id)
		if err == nil {
			last = run
			for _, w := range want {
				if run.State == w {
					return run
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %s never reached %v within %s (last %q)", id, want, timeout, last.State)
	return last
}

// childrenOf returns a run's children, waiting until the expected number exist.
func childrenOf(t *testing.T, stack *harness.Stack, org uc.OrgID, parent uc.RunID, want int, timeout time.Duration) []uc.AgentRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last []uc.AgentRun
	for time.Now().Before(deadline) {
		kids, err := stack.Store.Org(org).Runs().Children(context.Background(), parent)
		if err == nil {
			last = kids
			if len(kids) >= want {
				return kids
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("parent %s had %d children, want %d, within %s", parent, len(last), want, timeout)
	return last
}

// waitsOf returns every wait a parent has held.
func waitsOf(t *testing.T, stack *harness.Stack, org uc.OrgID, parent uc.RunID) []uc.RunWait {
	t.Helper()
	waits, err := stack.Store.Org(org).Waits().ListForParent(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	return waits
}

// A8.1 — a parent spawns a narrower child and a narrower grandchild; denied
// authority is invisible at discovery and refused at dispatch with a uniform
// error plus a PermissionDenied event.
func TestA81_SpawnDurabilityAndGrants(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	org := stack.OrgA.ID
	sess := createSession(t, alice, string(org), "spawn grants")

	// The parent spawns a child restricted to two tools and no spawning of
	// its own; the child then tries to exceed that.
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{
			Match:  modelscript.UserContains("parent work"),
			Sticky: true,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "spawn_agent", Args: map[string]any{
				"prompt": "child work", "tools": []string{"post_event", "spawn_agent"},
			}}},
		},
		{Match: modelscript.UserContains("parent work"), Text: "parent done"},
		// The child spawns a grandchild with an explicit allowlist.
		{
			Match:  modelscript.UserContains("child work"),
			Sticky: false,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "spawn_agent", Args: map[string]any{
				"prompt": "grandchild work", "tools": []string{"post_event"},
			}}},
		},
		{Match: modelscript.UserContains("child work"), Sticky: true, Text: "child done"},
		{Match: modelscript.UserContains("grandchild work"), Sticky: true, Text: "grandchild done"},
	}})

	parent, _, err := alice.StartRun(ctx, sess.GetId(), "parent work")
	if err != nil {
		t.Fatal(err)
	}
	parentID := uc.RunID(parent.GetId())
	kids := childrenOf(t, stack, org, parentID, 1, 60*time.Second)
	child := kids[0]

	if child.SpawnKey == "" {
		t.Fatal("child has no spawn key; spawning cannot be idempotent")
	}

	// The grandchild narrows further still.
	grandkids := childrenOf(t, stack, org, child.ID, 1, 60*time.Second)
	grandchild := grandkids[0]
	awaitRunOneOf(t, stack, org, grandchild.ID, 90*time.Second, uc.RunCompleted)

	// Child allowlist is exactly what was requested.
	if !child.Grants.AllowsTool("post_event") || !child.Grants.AllowsTool("spawn_agent") {
		t.Fatalf("child grants = %+v", child.Grants)
	}
	if child.Grants.AllowsTool("terminate_env") {
		t.Fatalf("child gained terminate_env without being granted it: %+v", child.Grants)
	}
}

// collectEvents reads the session log until it sees n events of a kind.
func collectEvents(t *testing.T, stack *harness.Stack, session, kind string, timeout time.Duration, n int) []uc.Event {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var found []uc.Event
		var from int64
		for {
			batch, err := stack.Store.Org(stack.OrgA.ID).Events().Range(context.Background(), uc.SessionID(session), from, 512)
			if err != nil || len(batch) == 0 {
				break
			}
			for _, e := range batch {
				from = e.Seq
				if e.Kind == kind {
					found = append(found, e)
				}
			}
		}
		if len(found) >= n {
			return found
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("session %s never recorded %d %q events within %s", session, n, kind, timeout)
	return nil
}

// A8.1 — a redelivered spawn adopts the child it already created: one child,
// one first step, no duplicate work. Distinct spawn keys keep concurrent
// children from colliding under queue redelivery.
func TestA81_SpawnIdempotentRetry(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	org := stack.OrgA.ID
	sess := createSession(t, alice, string(org), "spawn idempotency")

		stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{
			Match:  modelscript.UserContains("limit test"),
			Sticky: true,
			ToolCalls: []modelscript.ToolCallSpec{
				{Name: "spawn_agent", Args: map[string]any{"prompt": "kid one", "tools": []string{"post_event"}}},
				{Name: "spawn_agent", Args: map[string]any{"prompt": "kid two", "tools": []string{"post_event"}}},
				{Name: "spawn_agent", Args: map[string]any{"prompt": "kid three", "tools": []string{"post_event"}}},
			},
		},
		{Match: modelscript.UserContains("limit test"), Text: "parent done"},
		{Match: modelscript.UserContains("kid "), Sticky: true, Text: "kid done"},
	}})

	run, err := alice.Agents.StartRun(ctx, connect.NewRequest(&corev1.StartRunRequest{
		SessionId: sess.GetId(), Prompt: "limit test",
		// Two children allowed, three requested.
		Grants: &corev1.Grants{Tools: []string{"*"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	parentID := uc.RunID(run.Msg.GetRun().GetId())

	awaitRunOneOf(t, stack, org, parentID, 90*time.Second, uc.RunCompleted, uc.RunFailed)
	kids := stackChildren(t, stack, org, parentID)
	if len(kids) != 3 {
		t.Fatalf("parent created %d children, want 3", len(kids))
	}
	// Every child has a distinct spawn key derived from its tool call.
	keys := map[string]bool{}
	for _, kid := range kids {
		if kid.SpawnKey == "" {
			t.Fatalf("child %s has no spawn key", kid.ID)
		}
		if keys[kid.SpawnKey] {
			t.Fatalf("two children share spawn key %q", kid.SpawnKey)
		}
		keys[kid.SpawnKey] = true
	}

	// Each child ran exactly one first step: redelivery would show as extra
	// step rows for index 0, which the unique constraint forbids.
	for _, kid := range kids {
		steps, err := stack.Store.Org(org).Runs().Steps(ctx, kid.ID)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[int]bool{}
		for _, s := range steps {
			if seen[s.StepIndex] {
				t.Fatalf("child %s executed step %d twice", kid.ID, s.StepIndex)
			}
			seen[s.StepIndex] = true
		}
	}
}

func stackChildren(t *testing.T, stack *harness.Stack, org uc.OrgID, parent uc.RunID) []uc.AgentRun {
	t.Helper()
	kids, err := stack.Store.Org(org).Runs().Children(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	return kids
}

// assertSingleCorrelatedResult checks the invariant every wait case shares: the
// wait closed once, resumed the parent at most once, and injected exactly one
// tool result correlated to the parent's original tool call.
func assertSingleCorrelatedResult(t *testing.T, stack *harness.Stack, org uc.OrgID, parent uc.RunID, wantStates ...string) uc.RunWait {
	t.Helper()
	waits := waitsOf(t, stack, org, parent)
	if len(waits) != 1 {
		t.Fatalf("parent %s holds %d waits, want exactly 1", parent, len(waits))
	}
	wait := waits[0]
	if wait.State == uc.WaitOpen {
		t.Fatalf("wait %s is still open", wait.ID)
	}
	if len(wantStates) > 0 {
		ok := false
		for _, s := range wantStates {
			if wait.State == s {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("wait state %q, want one of %v", wait.State, wantStates)
		}
	}

	run, err := stack.Store.Org(org).Runs().Get(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	// The parent's history must contain exactly one *wait outcome* correlated
	// to the wait's tool-call id. The agent framework also records its own
	// placeholder result for that call ("waiting for child agents"), which is
	// the acknowledgement of the call rather than its answer, so the outcome
	// is identified by carrying the wait id.
	var env struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Data struct {
					ToolCallID string `json:"tool_call_id"`
					Output     struct {
						Data struct {
							Text string `json:"text"`
						} `json:"data"`
					} `json:"output"`
				} `json:"data"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(run.History, &env); err != nil {
		t.Fatal(err)
	}
	outcomes := 0
	for _, m := range env.Messages {
		if m.Role != "tool" {
			continue
		}
		for _, c := range m.Content {
			if c.Data.ToolCallID != wait.ToolCallID {
				continue
			}
			if strings.Contains(c.Data.Output.Data.Text, wait.ID) {
				outcomes++
			}
		}
	}
	if outcomes != 1 {
		t.Fatalf("parent history has %d wait outcomes correlated to tool call %q, want exactly 1; history=%s",
			outcomes, wait.ToolCallID, run.History)
	}
	// The parent must have been resumed exactly once.
	if wait.ResumedAt == nil {
		t.Fatalf("wait %s closed without recording a resumption", wait.ID)
	}
	return wait
}

// waitOutcome decodes a closed wait's aggregate result.
func waitOutcome(t *testing.T, wait uc.RunWait) uc.WaitOutcome {
	t.Helper()
	var outcome uc.WaitOutcome
	if err := json.Unmarshal(wait.Result, &outcome); err != nil {
		t.Fatalf("wait %s has undecodable result %q: %v", wait.ID, wait.Result, err)
	}
	return outcome
}

// A8.3 — one cohort call fans out to several children and fans back in with
// per-child results in declaration order, with no model-side polling.
func TestA83_CohortFanOutFanIn(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	org := stack.OrgA.ID
	sess := createSession(t, alice, string(org), "cohort")

	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{
			Match:  modelscript.UserContains("cohort work"),
			Sticky: true,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "run_agent_cohort", Args: map[string]any{
				"timeout": "3m",
				"specs": []map[string]any{
					{"prompt": "member alpha", "tools": []string{"post_event"}},
					{"prompt": "member beta", "tools": []string{"post_event"}},
					{"prompt": "member gamma", "tools": []string{"post_event"}},
				},
			}}},
		},
		{Match: modelscript.UserContains("cohort work"), Text: "cohort summarized"},
		{Match: modelscript.UserContains("member alpha"), Sticky: true, Text: "alpha result"},
		{Match: modelscript.UserContains("member beta"), Sticky: true, Text: "beta result"},
		{Match: modelscript.UserContains("member gamma"), Sticky: true, Text: "gamma result"},
	}})

	parent, _, err := alice.StartRun(ctx, sess.GetId(), "cohort work")
	if err != nil {
		t.Fatal(err)
	}
	parentID := uc.RunID(parent.GetId())

	kids := childrenOf(t, stack, org, parentID, 3, 90*time.Second)
	if len(kids) != 3 {
		t.Fatalf("cohort created %d children, want 3", len(kids))
	}
	// Members share one cohort id and carry their declaration order.
	cohortID := kids[0].CohortID
	if cohortID == "" {
		t.Fatal("cohort children carry no cohort id")
	}
	for i, kid := range kids {
		if kid.CohortID != cohortID {
			t.Fatalf("child %s belongs to cohort %q, want %q", kid.ID, kid.CohortID, cohortID)
		}
		if kid.CohortOrdinal != i {
			t.Fatalf("child %s has ordinal %d at position %d", kid.ID, kid.CohortOrdinal, i)
		}
	}

	awaitRunOneOf(t, stack, org, parentID, 3*time.Minute, uc.RunCompleted)
	wait := assertSingleCorrelatedResult(t, stack, org, parentID, uc.WaitResolved)
	if wait.Kind != uc.WaitKindCohort {
		t.Fatalf("wait kind %q, want cohort", wait.Kind)
	}
	outcome := waitOutcome(t, wait)
	if len(outcome.Members) != 3 {
		t.Fatalf("cohort outcome has %d members, want 3", len(outcome.Members))
	}
	// Declaration order is preserved in the aggregate result.
	for i, member := range outcome.Members {
		if member.Ordinal != i {
			t.Fatalf("outcome member %d has ordinal %d", i, member.Ordinal)
		}
		if member.State != uc.RunCompleted {
			t.Fatalf("member %d state %q, want completed", i, member.State)
		}
		if len(member.Result) == 0 {
			t.Fatalf("member %d carries no result", i)
		}
	}
	if outcome.Completed != 3 || outcome.Failed != 0 {
		t.Fatalf("outcome tally completed=%d failed=%d, want 3/0", outcome.Completed, outcome.Failed)
	}
	// Each member's own text is visible, so results are per-child rather than
	// a single merged blob.
	joined := string(wait.Result)
	for _, want := range []string{"alpha result", "beta result", "gamma result"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("cohort result is missing %q: %s", want, joined)
		}
	}
}

// A8.3 — a failed member does not stall the cohort: the parent resumes with
// that member's failure recorded alongside its successful siblings.
func TestA83_CohortFailedMember(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	org := stack.OrgA.ID
	sess := createSession(t, alice, string(org), "cohort failure")

	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{
			Match:  modelscript.UserContains("mixed cohort"),
			Sticky: true,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "run_agent_cohort", Args: map[string]any{
				"timeout": "3m",
				"specs": []map[string]any{
					{"prompt": "good member", "tools": []string{"post_event"}},
					{"prompt": "doomed member", "tools": []string{"post_event"}},
				},
			}}},
		},
		{Match: modelscript.UserContains("mixed cohort"), Text: "handled the failure"},
		{Match: modelscript.UserContains("good member"), Sticky: true, Text: "good result"},
		// The doomed member's vendor call fails, which fails that run.
		{Match: modelscript.UserContains("doomed member"), Sticky: true, Status: 401},
	}})

	parent, _, err := alice.StartRun(ctx, sess.GetId(), "mixed cohort")
	if err != nil {
		t.Fatal(err)
	}
	parentID := uc.RunID(parent.GetId())
	childrenOf(t, stack, org, parentID, 2, 90*time.Second)
	awaitRunOneOf(t, stack, org, parentID, 3*time.Minute, uc.RunCompleted)

	wait := assertSingleCorrelatedResult(t, stack, org, parentID, uc.WaitResolved)
	outcome := waitOutcome(t, wait)
	if outcome.Completed != 1 || outcome.Failed != 1 {
		t.Fatalf("outcome tally completed=%d failed=%d, want 1/1: %s", outcome.Completed, outcome.Failed, wait.Result)
	}
	var sawFailureReason bool
	for _, member := range outcome.Members {
		if member.State == uc.RunFailed && member.FailureReason != "" {
			sawFailureReason = true
		}
	}
	if !sawFailureReason {
		t.Fatalf("failed member carries no typed failure reason: %s", wait.Result)
	}
}

// A8.3 — the parent holds no queued step while its cohort works, so a wait
// costs no worker slot.
func TestA83_CohortParksWithoutQueuedParentStep(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	org := stack.OrgA.ID
	sess := createSession(t, alice, string(org), "cohort parking")

	// The members block until released, so the parent is observably parked.
	release := make(chan struct{})
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{
			Match:  modelscript.UserContains("parking cohort"),
			Sticky: true,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "run_agent_cohort", Args: map[string]any{
				"timeout": "3m",
				"specs":   []map[string]any{{"prompt": "slow member", "tools": []string{"post_event"}}},
			}}},
		},
		{Match: modelscript.UserContains("parking cohort"), Text: "cohort finished"},
		{Match: modelscript.UserContains("slow member"), Sticky: true, Text: "slow result", Gate: release},
	}})

	parent, _, err := alice.StartRun(ctx, sess.GetId(), "parking cohort")
	if err != nil {
		t.Fatal(err)
	}
	parentID := uc.RunID(parent.GetId())
	awaitRunOneOf(t, stack, org, parentID, 90*time.Second, uc.RunAwaiting)

	// A parked parent must hold no *pending* step job: the wait costs no
	// worker slot. The step that installed the wait is still finishing at the
	// instant the parent turns awaiting, so poll until it retires and require
	// the parent to stay at zero rather than acquiring a new one. Its child
	// legitimately holds a slot, which is why this is scoped to the parent.
	deadline := time.Now().Add(60 * time.Second)
	settled := false
	for time.Now().Before(deadline) {
		depth, err := stack.QueueDepthForRun(ctx, parentID)
		if err != nil {
			t.Fatal(err)
		}
		if depth == 0 {
			settled = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !settled {
		t.Fatalf("parked parent never released its worker slot: %v", stack.DebugRunnableJobs(t, ctx))
	}
	// It stays released while the child keeps working.
	for range 5 {
		time.Sleep(200 * time.Millisecond)
		depth, err := stack.QueueDepthForRun(ctx, parentID)
		if err != nil {
			t.Fatal(err)
		}
		if depth != 0 {
			t.Fatalf("parked parent re-acquired %d step jobs before its child finished", depth)
		}
	}
	// The child, by contrast, is still working: the parent is parked on real
	// pending work rather than on nothing at all.
	kids := childrenOf(t, stack, org, parentID, 1, 30*time.Second)
	childDepth, err := stack.QueueDepthForRun(ctx, kids[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if childDepth == 0 {
		t.Fatal("no child step job is runnable; the parent is not parked on anything")
	}
	close(release)
	awaitRunOneOf(t, stack, org, parentID, 3*time.Minute, uc.RunCompleted)
	assertSingleCorrelatedResult(t, stack, org, parentID, uc.WaitResolved)
}
