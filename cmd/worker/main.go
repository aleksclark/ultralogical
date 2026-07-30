// worker executes durable queue jobs: agent-run steps (and, in later
// phases, env lifecycle). Stateless and horizontally scalable — any worker
// can resume any run from its persisted history. Configuration:
//
//	DATABASE_URL         Postgres connection string (required)
//	ULTRA_MASTER_KEY     32-byte hex credential master key (required)
//	ULTRA_JOB_TIMEOUT    per-job timeout (default 2m)
//	ULTRA_RESCUE_AFTER   rescue stuck jobs after (default job timeout + 30s)
//	ULTRA_MAX_WORKERS    per-process concurrency (default 10)
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aleksclark/ultralogical/jobqueue"
	riverqueue "github.com/aleksclark/ultralogical/jobqueue/river"
	"github.com/aleksclark/ultralogical/loop"
	"github.com/aleksclark/ultralogical/postgres"
	"github.com/aleksclark/ultralogical/secrets"
)

func main() {
	log := slog.New(secrets.NewRedactingHandler(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(log); err != nil {
		log.Error("worker exited", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	keyring, err := secrets.NewAESKeyring(os.Getenv("ULTRA_MASTER_KEY"))
	if err != nil {
		return err
	}

	store, pool, err := postgres.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	jobTimeout := envDuration("ULTRA_JOB_TIMEOUT", 2*time.Minute)
	rescueAfter := envDuration("ULTRA_RESCUE_AFTER", 0)
	if rescueAfter > 0 && rescueAfter <= jobTimeout {
		return errors.New("ULTRA_RESCUE_AFTER must be greater than ULTRA_JOB_TIMEOUT")
	}
	queue, err := riverqueue.New(ctx, pool, riverqueue.Config{
		MaxWorkers:  envInt("ULTRA_MAX_WORKERS", 10),
		JobTimeout:  jobTimeout,
		RescueAfter: rescueAfter,
	})
	if err != nil {
		return err
	}

	stepWorker := &loop.StepWorker{
		Store:    store,
		Keyring:  keyring,
		Enqueue:  postgres.TxEnqueuer{Queue: queue},
		Registry: loop.NewRegistry(),
		Log:      log,
	}
	jobqueue.Register(queue, jobqueue.Worker[loop.StepJob](stepWorker))

	if err := queue.Start(ctx); err != nil {
		return err
	}
	log.Info("worker started")

	<-ctx.Done()
	log.Info("draining")
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return queue.Stop(stopCtx)
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		var n int
		if _, err := parseInt(v, &n); err == nil {
			return n
		}
	}
	return def
}

func parseInt(s string, out *int) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(c-'0')
	}
	*out = n
	return n, nil
}

func envDuration(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
