package river

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river/rivertype"
)

// JobGet returns a River job row by id.
func (q *Queue) JobGet(ctx context.Context, id int64) (*rivertype.JobRow, error) {
	if q == nil || q.client == nil {
		return nil, fmt.Errorf("river: not initialized")
	}
	return q.client.JobGet(ctx, id)
}

// JobCancel cancels a River job by id.
func (q *Queue) JobCancel(ctx context.Context, id int64) (*rivertype.JobRow, error) {
	if q == nil || q.client == nil {
		return nil, fmt.Errorf("river: not initialized")
	}
	return q.client.JobCancel(ctx, id)
}

// JobRetry schedules a River job for immediate retry.
func (q *Queue) JobRetry(ctx context.Context, id int64) (*rivertype.JobRow, error) {
	if q == nil || q.client == nil {
		return nil, fmt.Errorf("river: not initialized")
	}
	return q.client.JobRetry(ctx, id)
}

// JobCancelTx cancels within a transaction.
func (q *Queue) JobCancelTx(ctx context.Context, tx pgx.Tx, id int64) (*rivertype.JobRow, error) {
	if q == nil || q.client == nil {
		return nil, fmt.Errorf("river: not initialized")
	}
	return q.client.JobCancelTx(ctx, tx, id)
}

// JobRetryTx retries within a transaction.
func (q *Queue) JobRetryTx(ctx context.Context, tx pgx.Tx, id int64) (*rivertype.JobRow, error) {
	if q == nil || q.client == nil {
		return nil, fmt.Errorf("river: not initialized")
	}
	return q.client.JobRetryTx(ctx, tx, id)
}
