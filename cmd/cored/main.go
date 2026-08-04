// cored is the ultracore API server: stateless, horizontally scalable,
// all state in Postgres. Configuration is environment-driven (CORE_*);
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

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/config"
	ultrahttp "github.com/aleksclark/ultracore/http"
	riverqueue "github.com/aleksclark/ultracore/jobqueue/river"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/provider"
	"github.com/aleksclark/ultracore/resourcework"
	"github.com/aleksclark/ultracore/secrets"
)

func main() {
	log := slog.New(secrets.NewRedactingHandler(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(log); err != nil {
		log.Error("cored exited", "error", err)
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
	addr := config.String("CORE_ADDR", ":8080")
	keyring, err := secrets.NewAESKeyring(os.Getenv("CORE_MASTER_KEY"))
	if err != nil {
		return err
	}

	if config.String("CORE_MIGRATE", "true") != "false" {
		if err := postgres.Migrate(ctx, databaseURL); err != nil {
			return err
		}
		log.Info("migrations applied")
	}

	store, pool, err := postgres.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	bus := postgres.NewEventBus(store, pool, log, 0)
	bus.Start()
	defer bus.Stop()

	queue, err := riverqueue.New(ctx, pool, riverqueue.Config{})
	if err != nil {
		return err
	}

	resources := &resourcework.Service{Store: store, Enqueue: postgres.TxEnqueuer{Queue: queue}, Keyring: keyring, Log: log}

	defaultModel := uc.ModelConfig{
		Provider:   config.String("CORE_DEFAULT_PROVIDER", "openai"),
		ModelID:    config.String("CORE_DEFAULT_MODEL", "gpt-4.1-mini"),
		Credential: "default",
	}

	providers := provider.StandardRegistry(providerDeployment())
	resources.Providers = providers
	handler := ultrahttp.NewHandler(ultrahttp.Config{
		Store:        store,
		Providers:    providers,
		Auth:         uc.NewAPIKeyAuthenticator(store),
		Bus:          bus,
		Log:          log,
		Keyring:      keyring,
		Enqueue:      postgres.TxEnqueuer{Queue: queue},
		DefaultModel: defaultModel,
		Resources:    resources,
		Ready: func() error {
			if err := pool.Ping(context.Background()); err != nil {
				return fmt.Errorf("postgres: %w", err)
			}
			return nil
		},
	})

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		Protocols:         protocols,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("cored listening", "addr", addr)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
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
