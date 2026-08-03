// cored is the ultracore API server: stateless, horizontally scalable,
// all state in Postgres. Configuration is environment-driven:
//
//	DATABASE_URL     Postgres connection string (required)
//	CORE_ADDR       listen address (default :8080)
//	CORE_DEV_TOKENS static dev auth, "token=email,token2=email2" (required
//	                 until real OIDC lands in Phase 7)
//	CORE_MIGRATE    run migrations at startup (default true)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/envprovider"
	"github.com/aleksclark/ultracore/envwork"
	ultrahttp "github.com/aleksclark/ultracore/http"
	riverqueue "github.com/aleksclark/ultracore/jobqueue/river"
	"github.com/aleksclark/ultracore/postgres"
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	addr := os.Getenv("CORE_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	devTokens := uc.ParseDevTokens(os.Getenv("CORE_DEV_TOKENS"))
	if len(devTokens) == 0 {
		return errors.New("CORE_DEV_TOKENS is required (no other authenticator is configured yet)")
	}
	keyring, err := secrets.NewAESKeyring(os.Getenv("CORE_MASTER_KEY"))
	if err != nil {
		return err
	}

	if os.Getenv("CORE_MIGRATE") != "false" {
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

	// Enqueue-only queue handle: cored inserts step jobs; workers run them.
	queue, err := riverqueue.New(ctx, pool, riverqueue.Config{})
	if err != nil {
		return err
	}

	envs := &envwork.Service{Store: store, Enqueue: postgres.TxEnqueuer{Queue: queue}, Keyring: keyring, Log: log}

	defaultModel := uc.ModelConfig{
		Provider:   envOr("CORE_DEFAULT_PROVIDER", "openai"),
		ModelID:    envOr("CORE_DEFAULT_MODEL", "gpt-4.1-mini"),
		Credential: "default",
	}

	// Registration probes the real control plane through this registry, so a
	// stored provider is one that answered rather than one that parsed.
	providers := envprovider.StandardRegistry(providerDeployment())
	handler := ultrahttp.NewHandler(ultrahttp.Config{
		Store:        store,
		Providers:    providers,
		Auth:         uc.NewDevTokenAuthenticator(store, devTokens),
		Bus:          bus,
		Log:          log,
		Keyring:      keyring,
		Enqueue:      postgres.TxEnqueuer{Queue: queue},
		DefaultModel: defaultModel,
		Envs:         envs,
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

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// providerDeployment reads which provider kinds this deployment offers and how
// environments are reached. Defaults keep a single-machine deployment working
// with no configuration.
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

// envInt32 reads a bounded numeric setting, treating anything unparseable as
// unset rather than as zero.
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
