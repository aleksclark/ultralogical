// Package jobqueue defines the durable job queue seam. No backend types leak
// through this package: implementations (river, inproc, and later pgq or
// others) live in subpackages and are validated by the shared conformance
// suite in jobqueue/conformance.
package jobqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Job is a queueable unit of work. Implementations must be plain structs with
// JSON-serializable fields and a stable Kind. Kind must be callable on the
// zero value.
type Job interface {
	Kind() string
}

// Options control enqueue behavior.
type Options struct {
	// MaxAttempts caps total delivery attempts (including the first).
	// Zero means the backend default.
	MaxAttempts int
	// ScheduledAt delays the first delivery until the given time.
	ScheduledAt time.Time
}

// Opt mutates enqueue Options.
type Opt func(*Options)

// WithMaxAttempts caps total delivery attempts.
func WithMaxAttempts(n int) Opt { return func(o *Options) { o.MaxAttempts = n } }

// WithScheduledAt delays first delivery.
func WithScheduledAt(t time.Time) Opt { return func(o *Options) { o.ScheduledAt = t } }

// Enqueuer enqueues jobs transactionally: a job enqueued via EnqueueTx becomes
// visible to workers if and only if tx commits. This is the mechanism that
// makes entity-creation + first-job atomic everywhere in the system.
type Enqueuer interface {
	EnqueueTx(ctx context.Context, tx pgx.Tx, job Job, opts ...Opt) error
}

// Worker processes jobs of one kind. Delivery is at-least-once: Work must be
// idempotent. Returning an error schedules a retry per backend policy.
type Worker[J Job] interface {
	Work(ctx context.Context, job J) error
}

// WorkerFunc adapts a function to Worker.
type WorkerFunc[J Job] func(ctx context.Context, job J) error

// Work implements Worker.
func (f WorkerFunc[J]) Work(ctx context.Context, job J) error { return f(ctx, job) }

// RawHandler processes an encoded job payload. Registrars dispatch to it by
// kind; the generic Register wrapper handles decoding.
type RawHandler func(ctx context.Context, payload []byte) error

// Registrar registers handlers by kind. Backends implement it; callers use
// the type-safe Register instead.
type Registrar interface {
	RegisterKind(kind string, h RawHandler)
}

// Queue is a runnable job queue backend.
type Queue interface {
	Enqueuer
	Registrar
	// Start begins claiming and working jobs. Register all kinds first.
	Start(ctx context.Context) error
	// Stop drains gracefully.
	Stop(ctx context.Context) error
}

// Register wires a typed Worker into a Registrar. Payloads are decoded as
// JSON into J's concrete type, keeping the seam type-safe at registration
// while backends store bytes.
func Register[J Job](r Registrar, w Worker[J]) {
	var zero J
	r.RegisterKind(zero.Kind(), func(ctx context.Context, payload []byte) error {
		var job J
		if err := json.Unmarshal(payload, &job); err != nil {
			return fmt.Errorf("jobqueue: decode %s: %w", zero.Kind(), err)
		}
		return w.Work(ctx, job)
	})
}

// Encode serializes a job payload for backends.
func Encode(job Job) ([]byte, error) {
	b, err := json.Marshal(job)
	if err != nil {
		return nil, fmt.Errorf("jobqueue: encode %s: %w", job.Kind(), err)
	}
	return b, nil
}
