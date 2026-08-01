package loop

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/jobqueue"
)

// PresenceReapJob marks participants idle after they stop sending heartbeats.
//
// Presence has to expire on its own: a browser tab that closes without leaving,
// or a worker that dies mid-run, would otherwise appear present forever. The
// reaper is a durable self-rescheduling job so expiry survives the death of any
// particular process.
type PresenceReapJob struct {
	OrgID string `json:"org_id"`
}

// Kind implements jobqueue.Job.
func (PresenceReapJob) Kind() string { return "presence.reap" }

// PresenceReaper expires stale presence for one org.
type PresenceReaper struct {
	Store   ultra.Store
	Enqueue jobqueue.TxEnqueuer
	Log     *slog.Logger
	// After is how long without a heartbeat before a participant is idle.
	After time.Duration
	// Interval is how often the reaper runs.
	Interval time.Duration
	// Batch bounds one pass.
	Batch int
}

// DefaultPresenceAfter is the documented idle threshold: a participant that
// has not been heard from for this long is shown as idle rather than active.
const DefaultPresenceAfter = 45 * time.Second

func (r *PresenceReaper) after() time.Duration {
	if r.After > 0 {
		return r.After
	}
	return DefaultPresenceAfter
}

func (r *PresenceReaper) interval() time.Duration {
	if r.Interval > 0 {
		return r.Interval
	}
	return 15 * time.Second
}

func (r *PresenceReaper) batch() int {
	if r.Batch > 0 {
		return r.Batch
	}
	return 64
}

// Arm schedules the first pass for an org. It is idempotent in effect: an extra
// pass finds nothing to reap.
func (r *PresenceReaper) Arm(ctx context.Context, org ultra.OrgID) error {
	return r.Store.Tx(ctx, func(txs ultra.Store) error {
		return r.Enqueue.EnqueueInTx(ctx, txs, PresenceReapJob{OrgID: string(org)},
			jobqueue.WithScheduledAt(time.Now().Add(r.interval())))
	})
}

// Reap implements jobqueue.Worker[PresenceReapJob]. It transitions stale
// participants to idle, appends the typed transition event so subscribers see
// it, and re-arms itself.
func (r *PresenceReaper) Reap(ctx context.Context, job PresenceReapJob) error {
	org := ultra.OrgID(job.OrgID)
	cutoff := time.Now().Add(-r.after())
	// The claim and its events commit together, so a participant can never be
	// idle in the table without the log saying so.
	err := r.Store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		reaped, err := scope.Participants().ReapIdle(ctx, cutoff, r.batch())
		if err != nil {
			return err
		}
		for _, p := range reaped {
			payload, err := json.Marshal(ultra.ParticipantEventPayload{
				Kind: p.Kind, ParticipantID: p.ParticipantID, Display: p.Display,
			})
			if err != nil {
				return err
			}
			if _, err := scope.Events().Append(ctx, p.SessionID, ultra.Event{
				Actor:   ultra.Actor{Type: ultra.ActorSystem},
				Kind:    ultra.EventKindParticipantIdle,
				Payload: payload,
			}); err != nil {
				return err
			}
			if r.Log != nil {
				r.Log.Info("loop: presence expired", "session", string(p.SessionID),
					"participant", p.ParticipantID)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Re-arm unconditionally: the reaper is the only thing expiring presence,
	// so it must survive empty passes.
	return r.Arm(ctx, org)
}
