// Package river implements jobqueue.Queue on riverqueue.com's River, a
// Postgres-native durable job queue. All jobs are inserted as a single River
// kind wrapping an envelope (our kind + payload) so the seam's dynamic kind
// registration maps onto River's static worker typing. No river types leak
// out of this package.
package river

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	riverlib "github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"

	"github.com/aleksclark/ultralogical/jobqueue"
)

// envelope carries our job kind and payload inside a single River job kind.
type envelope struct {
	JobKind string          `json:"job_kind"`
	Payload json.RawMessage `json:"payload"`
}

// Kind implements river.JobArgs.
func (envelope) Kind() string { return "ultra_envelope" }

// Config tunes the queue.
type Config struct {
	// MaxWorkers is the per-process concurrency. Default 10.
	MaxWorkers int
	// RetryDelay is the fixed delay between attempts. Default 1s.
	RetryDelay time.Duration
}

// Queue implements jobqueue.Queue on River.
type Queue struct {
	client *riverlib.Client[pgx.Tx]

	mu       sync.RWMutex
	handlers map[string]jobqueue.RawHandler
}

// fixedRetry is a deterministic retry policy so redelivery timing is a
// contract the conformance suite can assert, not an implementation accident.
type fixedRetry struct{ delay time.Duration }

func (p fixedRetry) NextRetry(*rivertype.JobRow) time.Time { return time.Now().Add(p.delay) }

type envelopeWorker struct {
	riverlib.WorkerDefaults[envelope]
	q *Queue
}

func (w *envelopeWorker) Work(ctx context.Context, job *riverlib.Job[envelope]) error {
	w.q.mu.RLock()
	h, ok := w.q.handlers[job.Args.JobKind]
	w.q.mu.RUnlock()
	if !ok {
		return fmt.Errorf("river: no handler for kind %q", job.Args.JobKind)
	}
	return h(ctx, job.Args.Payload)
}

// New migrates River's schema and creates a Queue on the shared pool.
func New(ctx context.Context, pool *pgxpool.Pool, cfg Config) (*Queue, error) {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 10
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = time.Second
	}

	driver := riverpgxv5.New(pool)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		return nil, fmt.Errorf("river: migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, fmt.Errorf("river: migrate: %w", err)
	}

	q := &Queue{handlers: map[string]jobqueue.RawHandler{}}
	workers := riverlib.NewWorkers()
	riverlib.AddWorker(workers, &envelopeWorker{q: q})

	client, err := riverlib.NewClient(driver, &riverlib.Config{
		Queues: map[string]riverlib.QueueConfig{
			riverlib.QueueDefault: {MaxWorkers: cfg.MaxWorkers},
		},
		Workers:     workers,
		RetryPolicy: fixedRetry{delay: cfg.RetryDelay},
	})
	if err != nil {
		return nil, fmt.Errorf("river: client: %w", err)
	}
	q.client = client
	return q, nil
}

// RegisterKind implements jobqueue.Registrar.
func (q *Queue) RegisterKind(kind string, h jobqueue.RawHandler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[kind] = h
}

// EnqueueTx implements jobqueue.Enqueuer.
func (q *Queue) EnqueueTx(ctx context.Context, tx pgx.Tx, job jobqueue.Job, opts ...jobqueue.Opt) error {
	var o jobqueue.Options
	for _, opt := range opts {
		opt(&o)
	}
	payload, err := jobqueue.Encode(job)
	if err != nil {
		return err
	}
	insertOpts := &riverlib.InsertOpts{}
	if o.MaxAttempts > 0 {
		insertOpts.MaxAttempts = o.MaxAttempts
	}
	if !o.ScheduledAt.IsZero() {
		insertOpts.ScheduledAt = o.ScheduledAt
	}
	if _, err := q.client.InsertTx(ctx, tx, envelope{JobKind: job.Kind(), Payload: payload}, insertOpts); err != nil {
		return fmt.Errorf("river: insert: %w", err)
	}
	return nil
}

// Start implements jobqueue.Queue.
func (q *Queue) Start(ctx context.Context) error {
	if err := q.client.Start(ctx); err != nil {
		return fmt.Errorf("river: start: %w", err)
	}
	return nil
}

// Stop implements jobqueue.Queue.
func (q *Queue) Stop(ctx context.Context) error {
	if err := q.client.Stop(ctx); err != nil {
		return fmt.Errorf("river: stop: %w", err)
	}
	return nil
}
