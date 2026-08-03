// Package conformance is the shared black-box suite every jobqueue backend
// must pass. It asserts the seam's contract: transactional enqueue
// visibility, rollback invisibility, at-least-once redelivery with bounded
// retry accounting and backoff, panic redelivery, shutdown redelivery, and
// kind routing. New backends get added to the CI matrix by passing this
// suite — nothing else in the system may depend on backend-specific
// behavior.
package conformance

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aleksclark/ultracore/jobqueue"
)

// Factory builds a started queue for the suite and returns it along with the
// retry delay the backend promises between attempts (used to assert backoff).
type Factory func(t *testing.T) (q jobqueue.Queue, retryDelay time.Duration)

type testJob struct {
	Marker string `json:"marker"`
}

func (testJob) Kind() string { return "conformance_test" }

type otherJob struct {
	Marker string `json:"marker"`
}

func (otherJob) Kind() string { return "conformance_other" }

// recorder collects worked markers with timestamps, thread-safe.
type recorder struct {
	mu     sync.Mutex
	seen   map[string][]time.Time
	failN  map[string]*atomic.Int32 // remaining failures per marker
	panicN map[string]*atomic.Int32
}

func newRecorder() *recorder {
	return &recorder{seen: map[string][]time.Time{}, failN: map[string]*atomic.Int32{}, panicN: map[string]*atomic.Int32{}}
}

func (r *recorder) failFirst(marker string, n int32) {
	c := &atomic.Int32{}
	c.Store(n)
	r.mu.Lock()
	r.failN[marker] = c
	r.mu.Unlock()
}

func (r *recorder) panicFirst(marker string, n int32) {
	c := &atomic.Int32{}
	c.Store(n)
	r.mu.Lock()
	r.panicN[marker] = c
	r.mu.Unlock()
}
func (r *recorder) record(marker string) error {
	r.mu.Lock()
	r.seen[marker] = append(r.seen[marker], time.Now())
	c := r.failN[marker]
	p := r.panicN[marker]
	r.mu.Unlock()
	if p != nil && p.Add(-1) >= 0 {
		panic("conformance: induced panic")
	}
	if c != nil && c.Add(-1) >= 0 {
		return errors.New("conformance: induced failure")
	}
	return nil
}

func (r *recorder) attempts(marker string) []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]time.Time, len(r.seen[marker]))
	copy(out, r.seen[marker])
	return out
}

func (r *recorder) waitAttempts(t *testing.T, marker string, n int, timeout time.Duration) []time.Time {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := r.attempts(marker); len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := r.attempts(marker)
	t.Fatalf("marker %q: wanted %d attempts within %s, got %d", marker, n, timeout, len(got))
	return got
}

// Run executes the conformance suite against a backend. The pool must point
// at the same database the queue enqueues through.
func Run(t *testing.T, pool *pgxpool.Pool, factory Factory) {
	ctx := context.Background()

	t.Run("TransactionalVisibility", func(t *testing.T) {
		q, _ := factory(t)
		rec := newRecorder()
		jobqueue.Register(q, jobqueue.WorkerFunc[testJob](func(_ context.Context, j testJob) error {
			return rec.record(j.Marker)
		}))

		// Uncommitted: hold the tx open, assert nothing is worked.
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.EnqueueTx(ctx, tx, testJob{Marker: "committed"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		if got := rec.attempts("committed"); len(got) != 0 {
			t.Fatalf("job visible before commit: %d attempts", len(got))
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		rec.waitAttempts(t, "committed", 1, 10*time.Second)

		// Rolled back: never delivered.
		tx2, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.EnqueueTx(ctx, tx2, testJob{Marker: "rolledback"}); err != nil {
			t.Fatal(err)
		}
		if err := tx2.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		if got := rec.attempts("rolledback"); len(got) != 0 {
			t.Fatalf("rolled-back job was delivered: %d attempts", len(got))
		}
	})

	t.Run("RollbackInvisibility", func(t *testing.T) {
		// A rolled back transaction must leave no trace: not delivered
		// now, and not resurrected later by any backend maintenance.
		q, _ := factory(t)
		rec := newRecorder()
		jobqueue.Register(q, jobqueue.WorkerFunc[testJob](func(_ context.Context, j testJob) error {
			return rec.record(j.Marker)
		}))

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.EnqueueTx(ctx, tx, testJob{Marker: "never-committed"}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		// A committed control job proves the queue is live during the
		// window, so "nothing was delivered" is not a false negative.
		enqueue(t, pool, q, testJob{Marker: "control"})
		rec.waitAttempts(t, "control", 1, 10*time.Second)
		time.Sleep(2 * time.Second)
		if got := rec.attempts("never-committed"); len(got) != 0 {
			t.Fatalf("rolled-back job delivered %d times", len(got))
		}
	})

	t.Run("RetryAttemptAccounting", func(t *testing.T) {
		// A permanently failing job is attempted exactly MaxAttempts
		// times: at-least-once delivery is bounded, not infinite.
		q, retryDelay := factory(t)
		rec := newRecorder()
		rec.failFirst("bounded", 1000)
		jobqueue.Register(q, jobqueue.WorkerFunc[testJob](func(_ context.Context, j testJob) error {
			return rec.record(j.Marker)
		}))

		enqueue(t, pool, q, testJob{Marker: "bounded"}, jobqueue.WithMaxAttempts(3))
		attempts := rec.waitAttempts(t, "bounded", 3, 60*time.Second)
		if len(attempts) != 3 {
			t.Fatalf("wanted exactly 3 attempts, got %d", len(attempts))
		}
		// Every retry respects the promised minimum backoff.
		for i := 1; i < len(attempts); i++ {
			if gap := attempts[i].Sub(attempts[i-1]); gap < retryDelay/2 {
				t.Fatalf("retry gap %d = %s, below promised delay %s", i, gap, retryDelay)
			}
		}
		// No further deliveries once the attempt budget is exhausted.
		time.Sleep(4 * retryDelay)
		if got := rec.attempts("bounded"); len(got) != 3 {
			t.Fatalf("job delivered %d times, want 3 (MaxAttempts not enforced)", len(got))
		}
	})

	t.Run("ShutdownRedelivery", func(t *testing.T) {
		// A job that failed on one queue instance survives that
		// instance's shutdown and is redelivered to a replacement
		// instance on the same database — the contract that lets any
		// worker resume any job after a deploy or crash.
		first, _ := factory(t)
		rec := newRecorder()
		rec.failFirst("survives-shutdown:first", 1000)
		jobqueue.Register(first, jobqueue.WorkerFunc[testJob](func(_ context.Context, _ testJob) error {
			return rec.record("survives-shutdown:first")
		}))

		enqueue(t, pool, first, testJob{Marker: "survives-shutdown"}, jobqueue.WithMaxAttempts(20))
		rec.waitAttempts(t, "survives-shutdown:first", 1, 30*time.Second)

		stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := first.Stop(stopCtx); err != nil {
			t.Fatalf("stop first queue: %v", err)
		}
		before := len(rec.attempts("survives-shutdown:first"))

		second, _ := factory(t)
		jobqueue.Register(second, jobqueue.WorkerFunc[testJob](func(_ context.Context, _ testJob) error {
			return rec.record("survives-shutdown:second")
		}))
		rec.waitAttempts(t, "survives-shutdown:second", 1, 60*time.Second)

		if after := len(rec.attempts("survives-shutdown:first")); after > before+1 {
			t.Fatalf("stopped queue kept working jobs: %d → %d attempts", before, after)
		}
	})

	t.Run("AtLeastOnceRedelivery", func(t *testing.T) {
		q, retryDelay := factory(t)
		rec := newRecorder()
		rec.failFirst("retry-me", 1)
		jobqueue.Register(q, jobqueue.WorkerFunc[testJob](func(_ context.Context, j testJob) error {
			return rec.record(j.Marker)
		}))

		enqueue(t, pool, q, testJob{Marker: "retry-me"})
		attempts := rec.waitAttempts(t, "retry-me", 2, 30*time.Second)

		if len(attempts) < 2 {
			t.Fatalf("wanted >= 2 attempts, got %d", len(attempts))
		}
		// Backoff contract: second attempt respects the promised minimum
		// delay (with scheduling slack).
		gap := attempts[1].Sub(attempts[0])
		if gap < retryDelay/2 {
			t.Fatalf("retry gap %s below promised delay %s", gap, retryDelay)
		}
	})

	t.Run("PanicRedelivery", func(t *testing.T) {
		q, _ := factory(t)
		rec := newRecorder()
		rec.panicFirst("panic-me", 1)
		jobqueue.Register(q, jobqueue.WorkerFunc[testJob](func(_ context.Context, j testJob) error { return rec.record(j.Marker) }))
		enqueue(t, pool, q, testJob{Marker: "panic-me"})
		rec.waitAttempts(t, "panic-me", 2, 30*time.Second)
	})

	t.Run("KindRouting", func(t *testing.T) {
		q, _ := factory(t)
		rec := newRecorder()
		var wrongKind atomic.Int32
		jobqueue.Register(q, jobqueue.WorkerFunc[testJob](func(_ context.Context, j testJob) error {
			if j.Marker != "for-test" {
				wrongKind.Add(1)
			}
			return rec.record("test:" + j.Marker)
		}))
		jobqueue.Register(q, jobqueue.WorkerFunc[otherJob](func(_ context.Context, j otherJob) error {
			if j.Marker != "for-other" {
				wrongKind.Add(1)
			}
			return rec.record("other:" + j.Marker)
		}))

		enqueue(t, pool, q, testJob{Marker: "for-test"})
		enqueue(t, pool, q, otherJob{Marker: "for-other"})

		rec.waitAttempts(t, "test:for-test", 1, 10*time.Second)
		rec.waitAttempts(t, "other:for-other", 1, 10*time.Second)
		if wrongKind.Load() != 0 {
			t.Fatalf("worker received a job of the wrong kind")
		}
	})
}

func enqueue(t *testing.T, pool *pgxpool.Pool, q jobqueue.Queue, job jobqueue.Job, opts ...jobqueue.Opt) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueueTx(ctx, tx, job, opts...); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
