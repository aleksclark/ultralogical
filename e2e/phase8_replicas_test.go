package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/modelscript"
	"github.com/aleksclark/ultracore/testkit/testclient"
)

// eventKey identifies an event for cross-subscriber comparison. Two
// subscribers must agree on sequence, kind, and payload, not merely on count.
type eventKey struct {
	Seq  int64
	Kind string
	Body string
}

func keyOf(ev *corev1.SessionEvent) eventKey {
	body, _ := json.Marshal(ev.GetPayload())
	return eventKey{Seq: ev.GetSeq(), Kind: testclient.Kind(ev), Body: string(body)}
}

// assertGapFree checks a delivered sequence is contiguous from its first seq
// and contains no duplicates, which is the resume-by-seq contract.
func assertGapFree(t *testing.T, label string, keys []eventKey) {
	t.Helper()
	if len(keys) == 0 {
		t.Fatalf("%s received no events", label)
	}
	seen := map[int64]bool{}
	for i, k := range keys {
		if seen[k.Seq] {
			t.Fatalf("%s delivered seq %d twice", label, k.Seq)
		}
		seen[k.Seq] = true
		if i > 0 && k.Seq != keys[i-1].Seq+1 {
			t.Fatalf("%s has a gap: seq %d follows %d", label, k.Seq, keys[i-1].Seq)
		}
	}
}

// A8.4 — a subscription opened on one replica sees work started on another,
// survives that replica restarting, and converges with a load-balanced client
// on one identical gap-free sequence.
func TestA84_CrossReplicaSubscription(t *testing.T) {
	stack := harness.Up(t, harness.WithReplicas(2, 2))

	// The harness must really be running two of each; otherwise this test
	// proves nothing about distribution.
	if got := len(stack.ReplicaBaseURLs); got != 2 {
		t.Fatalf("harness started %d cored replicas, want 2", got)
	}
	if got := stack.WorkerCount(); got != 2 {
		t.Fatalf("harness started %d workers, want 2", got)
	}
	for i, base := range stack.ReplicaBaseURLs {
		if !healthy(base) {
			t.Fatalf("cored replica %d at %s is not healthy", i, base)
		}
	}

	ctx := context.Background()
	replicaA := stack.ReplicaClient(0, harness.TokenAlice)
	replicaB := stack.ReplicaClient(1, harness.TokenAlice)
	ingress := stack.IngressClient(harness.TokenAlice)
	org := stack.OrgA.ID

	// Created through the ingress, so no client is pinned to a replica.
	sess := createSession(t, ingress, string(org), "cross replica")
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{Match: modelscript.UserContains("replica work"), Sticky: true,
			Text: "streamed across replicas", ChunkSize: 4},
	}})

	// Subscribe on replica A while the work is started on replica B.
	subA, err := replicaA.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subA.Close()

	if _, err := replicaB.AppendUserMessage(ctx, sess.GetId(), "replica work"); err != nil {
		t.Fatal(err)
	}
	run, _, err := replicaB.StartRun(ctx, sess.GetId(), "replica work")
	if err != nil {
		t.Fatal(err)
	}
	replicaB.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 90*time.Second)

	// Replica A's subscriber saw work that only replica B ever handled.
	live := subA.CollectUntil(t, 90*time.Second, isTerminalRunEvent)
	liveKeys := make([]eventKey, 0, len(live))
	for _, ev := range live {
		liveKeys = append(liveKeys, keyOf(ev))
	}
	assertGapFree(t, "replica A live subscription", liveKeys)

	// Restart replica A underneath its subscriber. A fresh client must resume
	// by seq and rebuild the identical sequence.
	stack.RestartUltrad(0)
	resumed := stack.ReplicaClient(0, harness.TokenAlice)
	subResumed, err := resumed.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subResumed.Close()
	afterRestart := subResumed.CollectUntil(t, 90*time.Second, isTerminalRunEvent)
	restartKeys := make([]eventKey, 0, len(afterRestart))
	for _, ev := range afterRestart {
		restartKeys = append(restartKeys, keyOf(ev))
	}
	assertGapFree(t, "replica A after restart", restartKeys)

	// And a load-balanced client, which may hit either replica per request,
	// sees the same thing.
	subIngress, err := ingress.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subIngress.Close()
	viaIngress := subIngress.CollectUntil(t, 90*time.Second, isTerminalRunEvent)
	ingressKeys := make([]eventKey, 0, len(viaIngress))
	for _, ev := range viaIngress {
		ingressKeys = append(ingressKeys, keyOf(ev))
	}
	assertGapFree(t, "ingress subscription", ingressKeys)

	// All three views must be byte-identical, not merely similar.
	if fmt.Sprint(restartKeys) != fmt.Sprint(liveKeys) {
		t.Fatalf("resumed subscription diverged from the live one:\nlive=%v\nresumed=%v", liveKeys, restartKeys)
	}
	if fmt.Sprint(ingressKeys) != fmt.Sprint(liveKeys) {
		t.Fatalf("load-balanced subscription diverged:\nlive=%v\ningress=%v", liveKeys, ingressKeys)
	}

	// Resuming from a midpoint returns exactly the tail, with no duplicates.
	if len(liveKeys) < 3 {
		t.Fatalf("sequence too short to test partial resume: %v", liveKeys)
	}
	from := liveKeys[1].Seq
	subTail, err := ingress.Subscribe(ctx, sess.GetId(), from)
	if err != nil {
		t.Fatal(err)
	}
	defer subTail.Close()
	tail := subTail.CollectUntil(t, 90*time.Second, isTerminalRunEvent)
	if len(tail) == 0 {
		t.Fatal("partial resume returned nothing")
	}
	if got := tail[0].GetSeq(); got != from+1 {
		t.Fatalf("resume from seq %d delivered seq %d first, want %d", from, got, from+1)
	}
}

// healthy reports whether an cored replica answers its health endpoint.
func healthy(base string) bool {
	resp, err := harness.Health(base)
	return err == nil && resp
}

// A8.5 — four concurrent multi-step runs execute across two workers. Killing
// one worker mid-step must not lose, duplicate, or stall any of them.
func TestA85_WorkerTakeover(t *testing.T) {
	stack := harness.Up(t, harness.WithReplicas(2, 2))
	if got := stack.WorkerCount(); got != 2 {
		t.Fatalf("harness started %d workers, want 2", got)
	}
	ctx := context.Background()
	org := stack.OrgA.ID
	ingress := stack.IngressClient(harness.TokenAlice)
	sess := createSession(t, ingress, string(org), "worker takeover")

	// Each run takes several steps, so a kill lands mid-workload rather than
	// between runs. post_event keeps the steps real work with observable
	// effects.
	const runs = 4
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{
			Match:      modelscript.UserContains("takeover"),
			ToolCalls:  []modelscript.ToolCallSpec{{Name: "post_event", Args: map[string]any{"text": "step one"}}},
			ChunkDelay: 150 * time.Millisecond,
		},
		{
			Match:      modelscript.UserContains("takeover"),
			ToolCalls:  []modelscript.ToolCallSpec{{Name: "post_event", Args: map[string]any{"text": "step two"}}},
			ChunkDelay: 150 * time.Millisecond,
		},
		{
			Match:      modelscript.UserContains("takeover"),
			ToolCalls:  []modelscript.ToolCallSpec{{Name: "post_event", Args: map[string]any{"text": "step three"}}},
			ChunkDelay: 150 * time.Millisecond,
		},
		{Match: modelscript.UserContains("takeover"), Sticky: true, Text: "takeover complete"},
	}})

	ids := make([]string, runs)
	var wg sync.WaitGroup
	for i := range runs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			run, _, err := ingress.StartRun(ctx, sess.GetId(), fmt.Sprintf("takeover run %d", i))
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = run.GetId()
		}(i)
	}
	wg.Wait()

	// Wait until the workload is genuinely in flight, then kill worker 0.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		progressed := 0
		for _, id := range ids {
			if id == "" {
				continue
			}
			steps, err := stack.Store.Org(org).Runs().Steps(ctx, uc.RunID(id))
			if err == nil && len(steps) > 0 {
				progressed++
			}
		}
		if progressed >= runs/2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	stack.KillWorkerAt(0)

	// The surviving worker must finish everything.
	for _, id := range ids {
		if id == "" {
			t.Fatal("a run was never started")
		}
		ingress.AwaitRunState(t, id, corev1.RunState_RUN_STATE_COMPLETED, 4*time.Minute)
	}

	// No run executed a step index twice: redelivery after the kill is
	// idempotent, enforced by the step table's uniqueness.
	for _, id := range ids {
		steps, err := stack.Store.Org(org).Runs().Steps(ctx, uc.RunID(id))
		if err != nil {
			t.Fatal(err)
		}
		if len(steps) < 4 {
			t.Fatalf("run %s recorded %d steps, want the scripted 4", id, len(steps))
		}
		seen := map[int]bool{}
		for _, s := range steps {
			if seen[s.StepIndex] {
				t.Fatalf("run %s executed step %d more than once", id, s.StepIndex)
			}
			seen[s.StepIndex] = true
			// Redelivery is bounded: a step retried without limit would show
			// as a large attempt count.
			if s.Attempt > 5 {
				t.Fatalf("run %s step %d reached attempt %d; redelivery is unbounded", id, s.StepIndex, s.Attempt)
			}
		}
	}

	// The session log has no gaps despite a worker dying mid-stream.
	sub, err := ingress.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var keys []eventKey
	terminal := 0
	for terminal < runs {
		ev, err := sub.Next()
		if err != nil {
			t.Fatalf("event stream ended after %d events with %d terminal runs", len(keys), terminal)
		}
		keys = append(keys, keyOf(ev))
		if isTerminalRunEvent(ev) {
			terminal++
		}
	}
	assertGapFree(t, "session log after worker takeover", keys)

	// The queue drains: nothing is left retrying forever.
	drainDeadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(drainDeadline) {
		depth, err := stack.QueueDepth(ctx, "agent.step")
		if err != nil {
			t.Fatal(err)
		}
		if depth == 0 {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	depth, _ := stack.QueueDepth(ctx, "agent.step")
	t.Fatalf("queue still holds %d runnable step jobs after the workload completed", depth)
}
