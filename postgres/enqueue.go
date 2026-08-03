package postgres

import (
	"context"
	"errors"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/jobqueue"
)

// TxEnqueuer bridges a jobqueue.Enqueuer into store transactions: jobs are
// enqueued on the same pgx transaction a transaction-bound Store is using,
// so entity writes and their follow-up jobs commit atomically.
type TxEnqueuer struct {
	Queue jobqueue.Enqueuer
}

// EnqueueInTx enqueues a job within the transaction bound to txStore. It
// fails if txStore is not a transaction-bound *Store.
func (e TxEnqueuer) EnqueueInTx(ctx context.Context, txStore uc.Store, job jobqueue.Job, opts ...jobqueue.Opt) error {
	ps, ok := txStore.(*Store)
	if !ok || ps.PgxTx() == nil {
		return errors.New("postgres: EnqueueInTx requires a transaction-bound store")
	}
	return e.Queue.EnqueueTx(ctx, ps.PgxTx(), job, opts...)
}
