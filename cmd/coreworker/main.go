// coreworker executes durable queue jobs: agent-run steps and resource
// lifecycle. Stateless and horizontally scalable. Configuration is CORE_*;
// unknown CORE_* variables refuse startup. See docs/deploy.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aleksclark/ultracore/config"
	"github.com/aleksclark/ultracore/jobqueue"
	riverqueue "github.com/aleksclark/ultracore/jobqueue/river"
	"github.com/aleksclark/ultracore/loop"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/provider"
	"github.com/aleksclark/ultracore/resourcework"
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
	if err := config.RefuseUnknown(); err != nil {
		return err
	}

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

	// Health endpoints for orchestrators that probe workers. Opt-in via
	// CORE_ADDR so parallel test workers do not collide on a fixed port.
	if healthAddr := os.Getenv("CORE_ADDR"); healthAddr != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})
		mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
			if err := pool.Ping(context.Background()); err != nil {
				http.Error(w, fmt.Sprintf("postgres: %v", err), http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})
		go func() {
			if err := http.ListenAndServe(healthAddr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("health server", "error", err)
			}
		}()
		log.Info("worker health listening", "addr", healthAddr)
	}

	jobTimeout := config.Duration("CORE_JOB_TIMEOUT", 2*time.Minute)
	rescueAfter := config.Duration("CORE_RESCUE_AFTER", 0)
	if rescueAfter > 0 && rescueAfter <= jobTimeout {
		return errors.New("CORE_RESCUE_AFTER must be greater than CORE_JOB_TIMEOUT")
	}
	queue, err := riverqueue.New(ctx, pool, riverqueue.Config{
		MaxWorkers:  config.Int("CORE_MAX_WORKERS", 10),
		JobTimeout:  jobTimeout,
		RescueAfter: rescueAfter,
	})
	if err != nil {
		return err
	}

	registry := provider.StandardRegistry(providerDeployment())
	resources := &resourcework.Service{Store: store, Enqueue: postgres.TxEnqueuer{Queue: queue}, Keyring: keyring,
		Providers: registry, Log: log,
		ReconcileInterval: config.Duration("CORE_RECONCILE_INTERVAL", 5*time.Second),
		ProvisionTimeout:  config.Duration("CORE_PROVISION_TIMEOUT", time.Minute)}
	stepWorker := &loop.StepWorker{
		Store: store, Keyring: keyring, Enqueue: postgres.TxEnqueuer{Queue: queue},
		Registry: loop.NewRegistry(), Log: log, ToolResolver: &loop.ResourceTools{Store: store, Resources: resources},
	}
	waitSweeper := &loop.WaitSweeper{
		Store: store, Enqueue: postgres.TxEnqueuer{Queue: queue}, Worker: stepWorker, Log: log,
	}
	jobqueue.Register(queue, jobqueue.Worker[loop.StepJob](stepWorker))
	jobqueue.Register(queue, jobqueue.WorkerFunc[loop.WaitTimeoutJob](waitSweeper.Sweep))
	jobqueue.Register(queue, jobqueue.WorkerFunc[resourcework.ProvisionJob](resources.Provision))
	jobqueue.Register(queue, jobqueue.WorkerFunc[resourcework.TerminateJob](resources.Terminate))
	jobqueue.Register(queue, jobqueue.WorkerFunc[resourcework.ReconcileJob](resources.Reconcile))
	jobqueue.Register(queue, jobqueue.WorkerFunc[resourcework.RestartJob](resources.Restart))

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

func providerDeployment() provider.Deployment {
	deployment := provider.Deployment{
		BezalelImage:           config.String("CORE_BEZALEL_IMAGE", "ultracore/bezalel:local"),
		BezalelBinary:          os.Getenv("CORE_BEZALEL_BINARY"),
		KubernetesEndpointMode: config.String("CORE_K8S_ENDPOINT_MODE", ""),
		KubernetesEndpointHost: config.String("CORE_K8S_ENDPOINT_HOST", ""),
		EnabledKinds:           config.CSV("CORE_PROVIDER_KINDS"),
	}
	if low, high := config.Int32("CORE_K8S_NODEPORT_LOW"), config.Int32("CORE_K8S_NODEPORT_HIGH"); high > 0 {
		deployment.KubernetesNodePortRange = [2]int32{low, high}
	}
	return deployment
}
