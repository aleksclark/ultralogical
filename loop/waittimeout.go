package loop

import (
	"context"
	"log/slog"
	"time"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/jobqueue"
)

// WaitTimeoutJob sweeps waits whose deadline has passed.
//
// A timeout must be durable: a parent parked on children that never finish has
// to be released even if the process that created the wait is long gone, so
// the deadline lives in the database and any worker can enforce it. The job
// reschedules itself, which makes it self-healing after a crash.
type WaitTimeoutJob struct {
	OrgID string `json:"org_id"`
}

// Kind implements jobqueue.Job.
func (WaitTimeoutJob) Kind() string { return "wait.timeout" }

// WaitSweeper enforces wait deadlines for one org. A sweep is scheduled by
// wait creation itself, in the same transaction, so a wait can never exist
// without something scheduled to time it out.
type WaitSweeper struct {
	Store   uc.Store
	Enqueue jobqueue.TxEnqueuer
	Worker  *StepWorker
	Log     *slog.Logger
	// Batch bounds one sweep so a large backlog cannot monopolize a worker.
	Batch int
	// Retry is how long after an unfinished sweep to look again.
	Retry time.Duration
}

func (s *WaitSweeper) batch() int {
	if s.Batch > 0 {
		return s.Batch
	}
	return 32
}

func (s *WaitSweeper) retry() time.Duration {
	if s.Retry > 0 {
		return s.Retry
	}
	return 2 * time.Second
}

// Sweep implements jobqueue.Worker[WaitTimeoutJob]: it claims due waits and
// resolves each one.
//
// Claiming is itself a state transition, so a wait can only be timed out once
// even if two workers sweep simultaneously, and a child completing at the same
// instant either wins the row lock or finds the wait already closed.
func (s *WaitSweeper) Sweep(ctx context.Context, job WaitTimeoutJob) error {
	org := uc.OrgID(job.OrgID)
	due, err := s.Store.Org(org).Waits().ClaimDue(ctx, time.Now(), s.batch())
	if err != nil {
		return err
	}
	for _, wait := range due {
		waitID := wait.ID
		if err := s.Store.Tx(ctx, func(txs uc.Store) error {
			_, err := s.Worker.tryCloseWait(ctx, txs, org, waitID, closeReasonTimeout)
			return err
		}); err != nil {
			// The claim already moved this wait out of `open`, so returning an
			// error here would strand it. Re-arm instead and let the next
			// sweep finish the resolution.
			if s.Log != nil {
				s.Log.Error("loop: wait timeout resolution failed", "wait", waitID, "error", err)
			}
			return s.rearm(ctx, org)
		}
		if s.Log != nil {
			s.Log.Info("loop: wait timed out", "wait", waitID, "parent", string(wait.ParentRunID))
		}
	}
	// A full batch means more may be due right now.
	if len(due) == s.batch() {
		return s.rearm(ctx, org)
	}
	return nil
}

// rearm schedules another sweep shortly, used when a batch was truncated or a
// resolution needs retrying.
func (s *WaitSweeper) rearm(ctx context.Context, org uc.OrgID) error {
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		return s.Enqueue.EnqueueInTx(ctx, txs, WaitTimeoutJob{OrgID: string(org)},
			jobqueue.WithScheduledAt(time.Now().Add(s.retry())))
	})
}
