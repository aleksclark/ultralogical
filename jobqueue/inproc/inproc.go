// Package inproc is a minimal transactional job queue for tests. It stores
// jobs in a small Postgres table (so EnqueueTx is genuinely transactional —
// jobs become visible exactly when the surrounding transaction commits) and
// works them with an in-process polling loop. It is deliberately tiny and is
// validated by the same conformance suite as production backends.
package inproc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aleksclark/ultracore/jobqueue"
)

const schema = `
CREATE TABLE IF NOT EXISTS inproc_jobs (
    id           bigserial PRIMARY KEY,
    kind         text        NOT NULL,
    payload      jsonb       NOT NULL,
    attempts     int         NOT NULL DEFAULT 0,
    max_attempts int         NOT NULL DEFAULT 5,
    run_at       timestamptz NOT NULL DEFAULT now(),
    done         boolean     NOT NULL DEFAULT false
)`

// Config tunes the queue.
type Config struct {
	// PollInterval is how often the loop looks for ready jobs.
	PollInterval time.Duration
	// RetryDelay is the minimum delay before a failed job is redelivered.
	RetryDelay time.Duration
}

// Queue implements jobqueue.Queue on a shared Postgres pool.
type Queue struct {
	pool *pgxpool.Pool
	cfg  Config

	mu       sync.RWMutex
	handlers map[string]jobqueue.RawHandler

	cancel context.CancelFunc
	done   chan struct{}
}

// New creates a Queue. Defaults: 50ms poll, 100ms retry delay.
func New(pool *pgxpool.Pool, cfg Config) *Queue {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 50 * time.Millisecond
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = 100 * time.Millisecond
	}
	return &Queue{pool: pool, cfg: cfg, handlers: map[string]jobqueue.RawHandler{}}
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
	maxAttempts := o.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	runAt := o.ScheduledAt
	if runAt.IsZero() {
		runAt = time.Now()
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO inproc_jobs (kind, payload, max_attempts, run_at) VALUES ($1, $2, $3, $4)`,
		job.Kind(), payload, maxAttempts, runAt)
	if err != nil {
		return fmt.Errorf("inproc: enqueue: %w", err)
	}
	return nil
}

// Start creates the jobs table if needed and begins the work loop.
func (q *Queue) Start(ctx context.Context) error {
	if _, err := q.pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("inproc: ensure schema: %w", err)
	}
	loopCtx, cancel := context.WithCancel(context.Background())
	q.cancel = cancel
	q.done = make(chan struct{})
	go q.loop(loopCtx)
	return nil
}

// Stop implements jobqueue.Queue.
func (q *Queue) Stop(ctx context.Context) error {
	if q.cancel == nil {
		return nil
	}
	q.cancel()
	select {
	case <-q.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *Queue) loop(ctx context.Context) {
	defer close(q.done)
	ticker := time.NewTicker(q.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for q.workOne(ctx) {
			}
		}
	}
}

// workOne claims and processes a single ready job. Returns true if a job was
// processed (so the caller drains the backlog before sleeping again).
func (q *Queue) workOne(ctx context.Context) bool {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return false
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var (
		id      int64
		kind    string
		payload []byte
		attempt int
		maxAtt  int
	)
	err = tx.QueryRow(ctx,
		`SELECT id, kind, payload, attempts, max_attempts FROM inproc_jobs
		  WHERE NOT done AND run_at <= now()
		  ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).
		Scan(&id, &kind, &payload, &attempt, &maxAtt)
	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		return false
	}

	q.mu.RLock()
	h, ok := q.handlers[kind]
	q.mu.RUnlock()

	var workErr error
	if !ok {
		workErr = fmt.Errorf("inproc: no handler for kind %q", kind)
	} else {
		workErr = safeInvoke(ctx, h, payload)
	}

	if workErr == nil {
		_, err = tx.Exec(ctx, `UPDATE inproc_jobs SET done = true, attempts = attempts + 1 WHERE id = $1`, id)
	} else if attempt+1 >= maxAtt {
		_, err = tx.Exec(ctx, `UPDATE inproc_jobs SET done = true, attempts = attempts + 1 WHERE id = $1`, id)
	} else {
		_, err = tx.Exec(ctx,
			`UPDATE inproc_jobs SET attempts = attempts + 1, run_at = now() + $2::interval WHERE id = $1`,
			id, fmt.Sprintf("%d milliseconds", q.cfg.RetryDelay.Milliseconds()))
	}
	if err != nil {
		return false
	}
	return tx.Commit(ctx) == nil
}

// safeInvoke converts handler panics into errors so a panicking job is
// retried like a failing one (the at-least-once contract).
func safeInvoke(ctx context.Context, h jobqueue.RawHandler, payload []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("inproc: handler panic: %v", r)
		}
	}()
	return h(ctx, payload)
}
