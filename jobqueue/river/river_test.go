package river_test

import (
	"context"
	"testing"
	"time"

	"github.com/aleksclark/ultralogical/jobqueue"
	"github.com/aleksclark/ultralogical/jobqueue/conformance"
	riverqueue "github.com/aleksclark/ultralogical/jobqueue/river"
	"github.com/aleksclark/ultralogical/testkit/pgtest"
)

func TestConformance(t *testing.T) {
	pool, _ := pgtest.NewPool(t)
	conformance.Run(t, pool, func(t *testing.T) (jobqueue.Queue, time.Duration) {
		ctx := context.Background()
		retryDelay := 500 * time.Millisecond
		q, err := riverqueue.New(ctx, pool, riverqueue.Config{RetryDelay: retryDelay})
		if err != nil {
			t.Fatal(err)
		}
		if err := q.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = q.Stop(stopCtx)
		})
		return q, retryDelay
	})
}
