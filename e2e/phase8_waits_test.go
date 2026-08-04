package e2e

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/modelscript"
)

// A8.2 — the wait race matrix.
//
// Every case must resolve exactly one wait, inject exactly one outcome
// correlated to the parent's original tool call, and resume the parent at most
// once. Those invariants are checked by assertSingleCorrelatedResult, so each
// subtest only has to create its race.
//
// Nothing here sleeps to create ordering. Races are constructed with gated
// model turns, so the interleaving is chosen rather than hoped for.
func TestA82_WaitRaceMatrix(t *testing.T) {
	t.Run("child_terminal_before_wait_commit", func(t *testing.T) {
		// The child finishes before the parent's wait row exists. Without
		// in-transaction re-evaluation at creation, nothing would ever close
		// this wait and the parent would hang until its deadline.
		stack := harness.Up(t)
		alice := stack.AliceClient()
		ctx := context.Background()
		org := stack.TenantA.ID
		sess := createSession(t, alice, string(org), "wait pre-commit")

		// The parent spawns, then waits in a *later* step. By the time the
		// wait is created the child has long finished.
		spawnDone := make(chan struct{})
		stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
			{
				Match: modelscript.UserContains("precommit parent"),
				ToolCalls: []modelscript.ToolCallSpec{{Name: "spawn_agent", Args: map[string]any{
					"prompt": "precommit child", "tools": []string{"post_event"},
				}}},
			},
			// This turn is gated until the child is observed terminal, so the
			// wait is created strictly after the child finished.
			{
				Match: modelscript.UserContains("precommit parent"),
				Gate:  spawnDone,
				ToolCalls: []modelscript.ToolCallSpec{{Name: "wait_for_agents", Args: map[string]any{
					"run_ids": []string{waitOnAllChildren}, "timeout": "5m",
				}}},
			},
			{Match: modelscript.UserContains("precommit parent"), Text: "parent saw the child"},
			{Match: modelscript.UserContains("precommit child"), Sticky: true, Text: "child finished early"},
		}})

		parent, _, err := alice.StartRun(ctx, sess.GetId(), "precommit parent")
		if err != nil {
			t.Fatal(err)
		}
		parentID := uc.RunID(parent.GetId())
		kids := childrenOf(t, stack, org, parentID, 1, 90*time.Second)
		// Let the child reach a terminal state before the wait can be created.
		awaitRunOneOf(t, stack, org, kids[0].ID, 90*time.Second, uc.RunCompleted)
		close(spawnDone)

		awaitRunOneOf(t, stack, org, parentID, 2*time.Minute, uc.RunCompleted)
		wait := assertSingleCorrelatedResult(t, stack, org, parentID, uc.WaitResolved)
		outcome := waitOutcome(t, wait)
		if outcome.Completed != 1 || outcome.Pending != 0 {
			t.Fatalf("outcome completed=%d pending=%d, want 1/0: %s", outcome.Completed, outcome.Pending, wait.Result)
		}
	})

	t.Run("child_terminal_after_wait_commit", func(t *testing.T) {
		// The ordinary case: the parent parks first, the child finishes second.
		stack := harness.Up(t)
		alice := stack.AliceClient()
		ctx := context.Background()
		org := stack.TenantA.ID
		sess := createSession(t, alice, string(org), "wait post-commit")

		release := make(chan struct{})
		stack.Model.SetScript(cohortWaitScript("postcommit", release))
		parent, _, err := alice.StartRun(ctx, sess.GetId(), "postcommit parent")
		if err != nil {
			t.Fatal(err)
		}
		parentID := uc.RunID(parent.GetId())
		awaitRunOneOf(t, stack, org, parentID, 90*time.Second, uc.RunAwaiting)
		close(release)

		awaitRunOneOf(t, stack, org, parentID, 2*time.Minute, uc.RunCompleted)
		assertSingleCorrelatedResult(t, stack, org, parentID, uc.WaitResolved)
	})

	t.Run("duplicate_child_terminal_delivery", func(t *testing.T) {
		// Replaying the resolution must not resume the parent twice. The
		// database predicates, not process memory, are what enforce this, so
		// the test drives resolution again directly against the store.
		stack := harness.Up(t)
		alice := stack.AliceClient()
		ctx := context.Background()
		org := stack.TenantA.ID
		sess := createSession(t, alice, string(org), "wait duplicate")

		release := make(chan struct{})
		stack.Model.SetScript(cohortWaitScript("duplicate", release))
		parent, _, err := alice.StartRun(ctx, sess.GetId(), "duplicate parent")
		if err != nil {
			t.Fatal(err)
		}
		parentID := uc.RunID(parent.GetId())
		awaitRunOneOf(t, stack, org, parentID, 90*time.Second, uc.RunAwaiting)
		close(release)
		awaitRunOneOf(t, stack, org, parentID, 2*time.Minute, uc.RunCompleted)

		wait := assertSingleCorrelatedResult(t, stack, org, parentID, uc.WaitResolved)
		// A second close and a second resumption attempt must both be refused.
		scope := stack.Store.Tenant(org)
		if closed, err := scope.Waits().Close(ctx, wait.ID, uc.WaitResolved, wait.Result); err != nil {
			t.Fatal(err)
		} else if closed {
			t.Fatal("an already-closed wait accepted a second close")
		}
		if resumed, err := scope.Waits().MarkResumed(ctx, wait.ID); err != nil {
			t.Fatal(err)
		} else if resumed {
			t.Fatal("an already-resumed wait accepted a second resumption")
		}
		// The parent still holds exactly one outcome.
		assertSingleCorrelatedResult(t, stack, org, parentID, uc.WaitResolved)
	})

	t.Run("mixed_terminal_states", func(t *testing.T) {
		// Completed, failed, and cancelled children resolve one wait with each
		// child's own terminal state visible.
		stack := harness.Up(t)
		alice := stack.AliceClient()
		ctx := context.Background()
		org := stack.TenantA.ID
		sess := createSession(t, alice, string(org), "wait mixed")

		cancelReady := make(chan struct{})
		stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
			{
				Match: modelscript.UserContains("mixed parent"),
				ToolCalls: []modelscript.ToolCallSpec{{Name: "run_agent_cohort", Args: map[string]any{
					"timeout": "5m",
					"specs": []map[string]any{
						{"prompt": "mixed good", "tools": []string{"post_event"}},
						{"prompt": "mixed bad", "tools": []string{"post_event"}},
						{"prompt": "mixed doomed", "tools": []string{"post_event"}},
					},
				}}},
			},
			{Match: modelscript.UserContains("mixed parent"), Text: "parent handled mixed results"},
			{Match: modelscript.UserContains("mixed good"), Sticky: true, Text: "good"},
			// This member's vendor call fails, failing that run.
			{Match: modelscript.UserContains("mixed bad"), Sticky: true, Status: 401},
			// This member is held open so the test can cancel it.
			{Match: modelscript.UserContains("mixed doomed"), Sticky: true, Text: "never returned", Gate: cancelReady},
		}})

		parent, _, err := alice.StartRun(ctx, sess.GetId(), "mixed parent")
		if err != nil {
			t.Fatal(err)
		}
		parentID := uc.RunID(parent.GetId())
		kids := childrenOf(t, stack, org, parentID, 3, 90*time.Second)

		// Cancel the held member; the other two settle on their own.
		var doomed uc.RunID
		for _, kid := range kids {
			if kid.Prompt == "mixed doomed" {
				doomed = kid.ID
			}
		}
		if doomed == "" {
			t.Fatal("cohort did not create the member intended for cancellation")
		}
		if _, err := alice.Agents.CancelRun(ctx, connect.NewRequest(&corev1.CancelRunRequest{RunId: string(doomed)})); err != nil {
			t.Fatal(err)
		}
		close(cancelReady)

		awaitRunOneOf(t, stack, org, parentID, 3*time.Minute, uc.RunCompleted)
		wait := assertSingleCorrelatedResult(t, stack, org, parentID, uc.WaitResolved)
		outcome := waitOutcome(t, wait)
		if len(outcome.Members) != 3 {
			t.Fatalf("outcome has %d members, want 3: %s", len(outcome.Members), wait.Result)
		}
		if outcome.Completed != 1 || outcome.Failed != 1 || outcome.Cancelled != 1 {
			t.Fatalf("tally completed=%d failed=%d cancelled=%d, want 1/1/1: %s",
				outcome.Completed, outcome.Failed, outcome.Cancelled, wait.Result)
		}
	})

	t.Run("timeout_before_last_child", func(t *testing.T) {
		// The deadline passes while a child is still working. The parent must
		// be released with partial results rather than waiting forever.
		stack := harness.Up(t)
		alice := stack.AliceClient()
		ctx := context.Background()
		org := stack.TenantA.ID
		sess := createSession(t, alice, string(org), "wait timeout")

		neverReleased := make(chan struct{})
		stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
			{
				Match: modelscript.UserContains("timeout parent"),
				ToolCalls: []modelscript.ToolCallSpec{{Name: "run_agent_cohort", Args: map[string]any{
					// Short deadline: the member cannot finish in time.
					"timeout": "2s",
					"specs":   []map[string]any{{"prompt": "stuck member", "tools": []string{"post_event"}}},
				}}},
			},
			{Match: modelscript.UserContains("timeout parent"), Text: "parent proceeded without the member"},
			{Match: modelscript.UserContains("stuck member"), Sticky: true, Text: "too late", Gate: neverReleased},
		}})
		t.Cleanup(func() { close(neverReleased) })

		parent, _, err := alice.StartRun(ctx, sess.GetId(), "timeout parent")
		if err != nil {
			t.Fatal(err)
		}
		parentID := uc.RunID(parent.GetId())

		// The durable deadline releases the parent even though no child ever
		// finished, which is the whole point of the timeout being in the
		// database rather than in the process that created the wait.
		awaitRunOneOf(t, stack, org, parentID, 3*time.Minute, uc.RunCompleted)
		wait := assertSingleCorrelatedResult(t, stack, org, parentID, uc.WaitTimedOut)
		outcome := waitOutcome(t, wait)
		if !outcome.TimedOut {
			t.Fatalf("outcome does not report a timeout: %s", wait.Result)
		}
		if outcome.Pending != 1 {
			t.Fatalf("outcome pending=%d, want 1 unfinished member: %s", outcome.Pending, wait.Result)
		}
	})

	t.Run("timeout_races_last_child", func(t *testing.T) {
		// The last child finishes at almost exactly the deadline. Whichever
		// path wins, there must be exactly one resolution and one resumption.
		stack := harness.Up(t)
		alice := stack.AliceClient()
		ctx := context.Background()
		org := stack.TenantA.ID
		sess := createSession(t, alice, string(org), "wait timeout race")

		release := make(chan struct{})
		stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
			{
				Match: modelscript.UserContains("race parent"),
				ToolCalls: []modelscript.ToolCallSpec{{Name: "run_agent_cohort", Args: map[string]any{
					"timeout": "2s",
					"specs":   []map[string]any{{"prompt": "racing member", "tools": []string{"post_event"}}},
				}}},
			},
			{Match: modelscript.UserContains("race parent"), Text: "parent resumed once"},
			{Match: modelscript.UserContains("racing member"), Sticky: true, Text: "just in time", Gate: release},
		}})

		parent, _, err := alice.StartRun(ctx, sess.GetId(), "race parent")
		if err != nil {
			t.Fatal(err)
		}
		parentID := uc.RunID(parent.GetId())
		awaitRunOneOf(t, stack, org, parentID, 90*time.Second, uc.RunAwaiting)
		// Release the child right at the deadline so the sweeper and the
		// child's own resolution collide.
		time.Sleep(2 * time.Second)
		close(release)

		awaitRunOneOf(t, stack, org, parentID, 3*time.Minute, uc.RunCompleted)
		// Either outcome is legitimate; exactly one of them must have happened.
		assertSingleCorrelatedResult(t, stack, org, parentID, uc.WaitResolved, uc.WaitTimedOut)
	})

	t.Run("parent_cancelled", func(t *testing.T) {
		// Cancelling a parked parent must close its wait and prevent any later
		// child completion from resuming a run that is already terminal.
		stack := harness.Up(t)
		alice := stack.AliceClient()
		ctx := context.Background()
		org := stack.TenantA.ID
		sess := createSession(t, alice, string(org), "wait parent cancel")

		release := make(chan struct{})
		stack.Model.SetScript(cohortWaitScript("cancelled", release))
		parent, _, err := alice.StartRun(ctx, sess.GetId(), "cancelled parent")
		if err != nil {
			t.Fatal(err)
		}
		parentID := uc.RunID(parent.GetId())
		awaitRunOneOf(t, stack, org, parentID, 90*time.Second, uc.RunAwaiting)

		if _, err := alice.Agents.CancelRun(ctx, connect.NewRequest(&corev1.CancelRunRequest{RunId: string(parentID)})); err != nil {
			t.Fatal(err)
		}
		awaitRunOneOf(t, stack, org, parentID, 90*time.Second, uc.RunCancelled)

		// Now let the child finish. It must not revive the cancelled parent.
		close(release)
		kids := childrenOf(t, stack, org, parentID, 1, 30*time.Second)
		awaitRunOneOf(t, stack, org, kids[0].ID, 2*time.Minute, uc.RunCompleted)

		waits := waitsOf(t, stack, org, parentID)
		if len(waits) != 1 {
			t.Fatalf("parent holds %d waits, want 1", len(waits))
		}
		if waits[0].State == uc.WaitOpen {
			t.Fatalf("cancelled parent left an open wait: %+v", waits[0])
		}
		// And the parent stays cancelled rather than being resumed.
		final, err := stack.Store.Tenant(org).Runs().Get(ctx, parentID)
		if err != nil {
			t.Fatal(err)
		}
		if final.State != uc.RunCancelled {
			t.Fatalf("cancelled parent was revived into state %q", final.State)
		}
		// Any step job still queued for a cancelled run must be acknowledged
		// without executing, so the queue drains rather than retrying forever.
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			depth, err := stack.QueueDepthForRun(ctx, parentID)
			if err != nil {
				t.Fatal(err)
			}
			if depth == 0 {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if depth, err := stack.QueueDepthForRun(ctx, parentID); err != nil {
			t.Fatal(err)
		} else if depth != 0 {
			t.Fatalf("cancelled parent still has %d queued step jobs: %v",
				depth, stack.DebugRunnableJobs(t, ctx))
		}
		// The cancellation is final: no step executed after it.
		steps, err := stack.Store.Tenant(org).Runs().Steps(ctx, parentID)
		if err != nil {
			t.Fatal(err)
		}
		if len(steps) > 1 {
			t.Fatalf("cancelled parent executed %d steps; it should not have resumed", len(steps))
		}
	})

	t.Run("worker_death_during_resolution", func(t *testing.T) {
		// The worker dies while the child's terminal transition is in flight.
		// Redelivery must still produce exactly one resolution and one resume.
		stack := harness.Up(t)
		alice := stack.AliceClient()
		ctx := context.Background()
		org := stack.TenantA.ID
		sess := createSession(t, alice, string(org), "wait worker death")

		release := make(chan struct{})
		stack.Model.SetScript(cohortWaitScript("crashing", release))
		parent, _, err := alice.StartRun(ctx, sess.GetId(), "crashing parent")
		if err != nil {
			t.Fatal(err)
		}
		parentID := uc.RunID(parent.GetId())
		awaitRunOneOf(t, stack, org, parentID, 90*time.Second, uc.RunAwaiting)

		// Kill the worker while the child is mid-step, then bring a new one
		// up: the child's step is redelivered and resolves the wait.
		stack.KillWorker()
		close(release)
		stack.StartWorker()

		awaitRunOneOf(t, stack, org, parentID, 3*time.Minute, uc.RunCompleted)
		assertSingleCorrelatedResult(t, stack, org, parentID, uc.WaitResolved)
	})
}

// waitOnAllChildren mirrors the wait tool's wildcard: it expands to every
// child of the calling run, so a script can wait without knowing generated
// run ids.
const waitOnAllChildren = "*"

// cohortWaitScript builds a parent that launches one gated cohort member and
// then summarizes, which is the shape most race cases need.
func cohortWaitScript(name string, release <-chan struct{}) modelscript.Script {
	parentPrompt := name + " parent"
	memberPrompt := name + " member"
	return modelscript.Script{Turns: []modelscript.Turn{
		{
			Match: modelscript.UserContains(parentPrompt),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "run_agent_cohort", Args: map[string]any{
				"timeout": "5m",
				"specs":   []map[string]any{{"prompt": memberPrompt, "tools": []string{"post_event"}}},
			}}},
		},
		{Match: modelscript.UserContains(parentPrompt), Text: name + " parent finished"},
		{Match: modelscript.UserContains(memberPrompt), Sticky: true, Text: name + " member finished", Gate: release},
	}}
}
