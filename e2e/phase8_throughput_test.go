package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/modelscript"
)

// The baseline workload. These constants are the workload definition: a
// change here invalidates every previously recorded baseline, so they are
// named and documented rather than inlined.
const (
	baselineSessions    = 3
	baselineRunsPerSes  = 4
	baselineStepsPerRun = 3
	baselineUltrad      = 2
	baselineWorkers     = 2
)

// Regression ceilings. These are deliberately far above any healthy result:
// their job is to catch an order-of-magnitude regression on unknown CI
// hardware, not to advertise a latency number. Real analysis happens by
// diffing the emitted artifact across runs on the same machine.
const (
	baselineRunLatencyCeiling = 3 * time.Minute
	baselineEventLagCeiling   = 45 * time.Second
)

// baselineQuantiles summarizes a duration sample set.
type baselineQuantiles struct {
	Samples int     `json:"samples"`
	MinMS   float64 `json:"min_ms"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	P99MS   float64 `json:"p99_ms"`
	MaxMS   float64 `json:"max_ms"`
}

// baselineReport is the machine-readable artifact. Everything needed to
// decide whether two reports are comparable lives in it: without the
// workload, hardware, and topology a latency number means nothing.
type baselineReport struct {
	Schema     string           `json:"schema"`
	RecordedAt string           `json:"recorded_at"`
	Workload   baselineWorkload `json:"workload"`
	Hardware   baselineHardware `json:"hardware"`
	Results    baselineResults  `json:"results"`
}

type baselineWorkload struct {
	Sessions       int    `json:"sessions"`
	RunsPerSession int    `json:"runs_per_session"`
	TotalRuns      int    `json:"total_runs"`
	StepsPerRun    int    `json:"steps_per_run"`
	UltradReplicas int    `json:"ultrad_replicas"`
	Workers        int    `json:"workers"`
	Queue          string `json:"queue"`
	Model          string `json:"model"`
}

type baselineHardware struct {
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	NumCPU    int    `json:"num_cpu"`
	GoVersion string `json:"go_version"`
	CI        bool   `json:"ci"`
}

type baselineResults struct {
	WallClockMS      float64           `json:"wall_clock_ms"`
	RunsPerSecond    float64           `json:"runs_per_second"`
	StepsPerSecond   float64           `json:"steps_per_second"`
	RunLatency       baselineQuantiles `json:"run_latency"`
	EventDeliveryLag baselineQuantiles `json:"event_delivery_lag"`
	StepsRecorded    int               `json:"steps_recorded"`
	StepRetries      int               `json:"step_retries"`
	EventsDelivered  int               `json:"events_delivered"`
}

// quantile returns the p-th percentile of a sorted duration slice using
// nearest-rank, which is well-defined for small samples.
func quantile(sorted []time.Duration, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return float64(sorted[rank]) / float64(time.Millisecond)
}

func summarize(samples []time.Duration) baselineQuantiles {
	if len(samples) == 0 {
		return baselineQuantiles{}
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return baselineQuantiles{
		Samples: len(sorted),
		MinMS:   float64(sorted[0]) / float64(time.Millisecond),
		P50MS:   quantile(sorted, 0.50),
		P95MS:   quantile(sorted, 0.95),
		P99MS:   quantile(sorted, 0.99),
		MaxMS:   float64(sorted[len(sorted)-1]) / float64(time.Millisecond),
	}
}

// A8.8 — run the documented workload on two cored and two workers, emit a
// machine-readable measurement artifact, and assert the invariants that must
// hold at any speed plus a generous regression ceiling.
func TestA88_ThroughputBaseline(t *testing.T) {
	stack := harness.Up(t, harness.WithReplicas(baselineUltrad, baselineWorkers))
	if got := len(stack.ReplicaBaseURLs); got != baselineUltrad {
		t.Fatalf("harness started %d cored replicas, want %d", got, baselineUltrad)
	}
	if got := stack.WorkerCount(); got != baselineWorkers {
		t.Fatalf("harness started %d workers, want %d", got, baselineWorkers)
	}

	ctx := context.Background()
	org := stack.TenantA.ID
	ingress := stack.IngressClient(stack.KeyA)

	// Two tool steps then a final answer: three steps per run, so the
	// measurement covers the whole durable loop (job claim, model call, tool
	// dispatch, event append, re-enqueue) rather than a single round trip.
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{
			Match:     modelscript.UserContains("baseline"),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "post_event", Args: map[string]any{"text": "baseline step one"}}},
		},
		{
			Match:     modelscript.UserContains("baseline"),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "post_event", Args: map[string]any{"text": "baseline step two"}}},
		},
		{Match: modelscript.UserContains("baseline"), Sticky: true, Text: "baseline complete"},
	}})

	sessions := make([]string, baselineSessions)
	for i := range sessions {
		sessions[i] = createSession(t, ingress, string(org), fmt.Sprintf("baseline %d", i)).GetId()
	}

	// A subscriber on each replica measures delivery lag as observed by a
	// client that did not start the work, which is the number that matters
	// for a distributed session.
	type lagCollector struct {
		lags   []time.Duration
		events int
	}
	var lagMu sync.Mutex
	collected := lagCollector{}
	subCtx, stopSubs := context.WithCancel(ctx)
	defer stopSubs()
	var subWG sync.WaitGroup
	for replica := range baselineUltrad {
		client := stack.ReplicaClient(replica, stack.KeyA)
		for _, sessID := range sessions {
			sub, err := client.Subscribe(subCtx, sessID, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			subWG.Add(1)
			go func() {
				defer subWG.Done()
				for {
					ev, err := sub.Next()
					if err != nil {
						return
					}
					received := time.Now()
					lagMu.Lock()
					collected.events++
					if ts := ev.GetTs(); ts != nil {
						collected.lags = append(collected.lags, received.Sub(ts.AsTime()))
					}
					lagMu.Unlock()
				}
			}()
		}
	}

	total := baselineSessions * baselineRunsPerSes
	latencies := make([]time.Duration, total)
	runIDs := make([]string, total)

	// Every run is started concurrently: the baseline measures the system
	// under simultaneous load across both replicas, not a serial drip.
	start := time.Now()
	var wg sync.WaitGroup
	for s := range baselineSessions {
		for r := range baselineRunsPerSes {
			idx := s*baselineRunsPerSes + r
			wg.Add(1)
			go func() {
				defer wg.Done()
				began := time.Now()
				run, _, err := ingress.StartRun(ctx, sessions[s], fmt.Sprintf("baseline work %d", idx))
				if err != nil {
					t.Error(err)
					return
				}
				runIDs[idx] = run.GetId()
				ingress.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_COMPLETED, baselineRunLatencyCeiling)
				latencies[idx] = time.Since(began)
			}()
		}
	}
	wg.Wait()
	wall := time.Since(start)

	// Give the fan-out a bounded moment to drain before reading lag samples,
	// then stop the subscribers deterministically.
	time.Sleep(2 * time.Second)
	stopSubs()
	subWG.Wait()

	// Invariant: every run finished, and none was lost or left behind.
	for i, id := range runIDs {
		if id == "" {
			t.Fatalf("baseline run %d never started", i)
		}
	}

	// Invariant: step indices are unique per run, so nothing was executed
	// twice, and the scripted step count was actually reached.
	stepsRecorded, retries := 0, 0
	for _, id := range runIDs {
		steps, err := stack.Store.Tenant(org).Runs().Steps(ctx, uc.RunID(id))
		if err != nil {
			t.Fatal(err)
		}
		if len(steps) < baselineStepsPerRun {
			t.Fatalf("run %s recorded %d steps, want at least %d", id, len(steps), baselineStepsPerRun)
		}
		seen := map[int]bool{}
		for _, s := range steps {
			if seen[s.StepIndex] {
				t.Fatalf("run %s executed step index %d twice", id, s.StepIndex)
			}
			seen[s.StepIndex] = true
			stepsRecorded++
			if s.Attempt > 1 {
				retries++
			}
		}
	}

	// Invariant: the queue is empty once the workload reports done. A
	// baseline taken while work is still draining is not a baseline.
	depth, err := stack.QueueDepth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth != 0 {
		t.Fatalf("queue still holds %d runnable jobs after every run completed", depth)
	}

	lagMu.Lock()
	lagSamples := append([]time.Duration(nil), collected.lags...)
	eventsDelivered := collected.events
	lagMu.Unlock()

	// Invariant: subscribers actually observed the workload. A lag
	// measurement over zero events would be vacuous.
	if eventsDelivered == 0 {
		t.Fatal("no events were delivered to any replica subscriber")
	}
	for _, lag := range lagSamples {
		if lag < -5*time.Second {
			t.Fatalf("event delivered %s before its own server timestamp; clocks are unusable for this measurement", -lag)
		}
	}

	runLatency := summarize(latencies)
	eventLag := summarize(lagSamples)

	report := baselineReport{
		Schema:     "ultracore.throughput_baseline.v1",
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
		Workload: baselineWorkload{
			Sessions:       baselineSessions,
			RunsPerSession: baselineRunsPerSes,
			TotalRuns:      total,
			StepsPerRun:    baselineStepsPerRun,
			UltradReplicas: baselineUltrad,
			Workers:        baselineWorkers,
			Queue:          "river on postgres, worker defaults",
			Model:          "scripted modelscript server (no network)",
		},
		Hardware: baselineHardware{
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
			NumCPU:    runtime.NumCPU(),
			GoVersion: runtime.Version(),
			CI:        os.Getenv("CI") != "",
		},
		Results: baselineResults{
			WallClockMS:      float64(wall) / float64(time.Millisecond),
			RunsPerSecond:    float64(total) / wall.Seconds(),
			StepsPerSecond:   float64(stepsRecorded) / wall.Seconds(),
			RunLatency:       runLatency,
			EventDeliveryLag: eventLag,
			StepsRecorded:    stepsRecorded,
			StepRetries:      retries,
			EventsDelivered:  eventsDelivered,
		},
	}

	blob, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Emit on stdout so CI can scrape it, and to a file when asked, so a
	// developer can diff two runs on the same machine.
	t.Logf("CORE_THROUGHPUT_BASELINE %s", blob)
	if path := os.Getenv("CORE_BASELINE_OUT"); path != "" {
		if err := os.WriteFile(path, blob, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Generous regression ceilings, asserted last so the artifact is always
	// recorded even when the ceiling trips.
	if report.Results.RunsPerSecond <= 0 {
		t.Fatalf("throughput was not positive: %v", report.Results.RunsPerSecond)
	}
	if p99 := time.Duration(runLatency.P99MS * float64(time.Millisecond)); p99 > baselineRunLatencyCeiling {
		t.Fatalf("p99 run latency %s exceeds the regression ceiling %s", p99, baselineRunLatencyCeiling)
	}
	if p99 := time.Duration(eventLag.P99MS * float64(time.Millisecond)); p99 > baselineEventLagCeiling {
		t.Fatalf("p99 event delivery lag %s exceeds the regression ceiling %s", p99, baselineEventLagCeiling)
	}
}
