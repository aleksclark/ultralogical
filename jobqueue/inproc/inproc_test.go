package inproc_test

import (
	"context"
	"testing"
	"time"

	"github.com/aleksclark/ultralogical/jobqueue"
	"github.com/aleksclark/ultralogical/jobqueue/conformance"
	"github.com/aleksclark/ultralogical/jobqueue/inproc"
	"github.com/aleksclark/ultralogical/testkit/pgtest"
)

func TestConformance(t *testing.T) {
	pool, _ := pgtest.NewPool(t)
	conformance.Run(t, pool, func(t *testing.T) (jobqueue.Queue, time.Duration) {
		retryDelay := 100 * time.Millisecond
		q := inproc.New(pool, inproc.Config{RetryDelay: retryDelay})
		if err := q.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = q.Stop(ctx)
		})
		return q, retryDelay
	})
}
