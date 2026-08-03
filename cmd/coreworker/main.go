// coreworker executes durable queue jobs: agent-run steps and env lifecycle.
// Stateless and horizontally scalable — any worker can resume any run from
// its persisted history. Configuration:
//
//	DATABASE_URL         Postgres connection string (required)
//	CORE_MASTER_KEY     32-byte hex credential master key (required)
//	CORE_JOB_TIMEOUT    per-job timeout (default 2m)
//	CORE_RESCUE_AFTER   rescue stuck jobs after (default job timeout + 30s)
//	CORE_MAX_WORKERS    per-process concurrency (default 10)
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aleksclark/ultracore/envprovider"
	"github.com/aleksclark/ultracore/envwork"
	"github.com/aleksclark/ultracore/jobqueue"
	riverqueue "github.com/aleksclark/ultracore/jobqueue/river"
	"github.com/aleksclark/ultracore/loop"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/secrets"
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
	keyring, err := secrets.NewAESKeyring(os.Getenv("CORE_MASTER_KEY"))
	if err != nil {
		return err
	}

	store, pool, err := postgres.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	jobTimeout := envDuration("CORE_JOB_TIMEOUT", 2*time.Minute)
	rescueAfter := envDuration("CORE_RESCUE_AFTER", 0)
	if rescueAfter > 0 && rescueAfter <= jobTimeout {
		return errors.New("CORE_RESCUE_AFTER must be greater than CORE_JOB_TIMEOUT")
	}
	queue, err := riverqueue.New(ctx, pool, riverqueue.Config{
		MaxWorkers:  envInt("CORE_MAX_WORKERS", 10),
		JobTimeout:  jobTimeout,
		RescueAfter: rescueAfter,
	})
	if err != nil {
		return err
	}

	// Adapters are built per registration from its own configuration, so a
	// worker never holds one provider standing in for another.
	registry := envprovider.StandardRegistry(providerDeployment())
	envs := &envwork.Service{Store: store, Enqueue: postgres.TxEnqueuer{Queue: queue}, Keyring: keyring,
		Providers: registry, Log: log,
		ReconcileInterval: envDuration("CORE_RECONCILE_INTERVAL", 5*time.Second),
		ProvisionTimeout:  envDuration("CORE_PROVISION_TIMEOUT", time.Minute)}
	stepWorker := &loop.StepWorker{
		Store: store, Keyring: keyring, Enqueue: postgres.TxEnqueuer{Queue: queue},
		Registry: loop.NewRegistry(), Log: log, ToolResolver: &loop.EnvTools{Store: store, Envs: envs},
	}
	// Wait deadlines are enforced by any worker, not by the process that
	// created the wait, so a parked parent is released even after a crash.
	waitSweeper := &loop.WaitSweeper{
		Store: store, Enqueue: postgres.TxEnqueuer{Queue: queue}, Worker: stepWorker, Log: log,
	}
	jobqueue.Register(queue, jobqueue.Worker[loop.StepJob](stepWorker))
	jobqueue.Register(queue, jobqueue.WorkerFunc[loop.WaitTimeoutJob](waitSweeper.Sweep))
	jobqueue.Register(queue, jobqueue.WorkerFunc[envwork.ProvisionJob](envs.Provision))
	jobqueue.Register(queue, jobqueue.WorkerFunc[envwork.TerminateJob](envs.Terminate))
	jobqueue.Register(queue, jobqueue.WorkerFunc[envwork.ReconcileJob](envs.Reconcile))
	jobqueue.Register(queue, jobqueue.WorkerFunc[envwork.RestartJob](envs.Restart))

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

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// providerDeployment reads which provider kinds this worker can host and how
// environments are reached.
func providerDeployment() envprovider.Deployment {
	deployment := envprovider.Deployment{
		BezalelImage:           envOr("CORE_BEZALEL_IMAGE", "ultracore/bezalel:local"),
		BezalelBinary:          os.Getenv("CORE_BEZALEL_BINARY"),
		KubernetesEndpointMode: envOr("CORE_K8S_ENDPOINT_MODE", ""),
		KubernetesEndpointHost: envOr("CORE_K8S_ENDPOINT_HOST", ""),
	}
	if kinds := os.Getenv("CORE_PROVIDER_KINDS"); kinds != "" {
		deployment.EnabledKinds = strings.Split(kinds, ",")
	}
	if low, high := envInt32("CORE_K8S_NODEPORT_LOW"), envInt32("CORE_K8S_NODEPORT_HIGH"); high > 0 {
		deployment.KubernetesNodePortRange = [2]int32{low, high}
	}
	return deployment
}

func envInt32(name string) int32 {
	value := os.Getenv(name)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0
	}
	return int32(parsed)
}
