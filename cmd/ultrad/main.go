// ultrad is the Ultralogical API server: stateless, horizontally scalable,
// all state in Postgres. Configuration is environment-driven:
//
//	DATABASE_URL     Postgres connection string (required)
//	ULTRA_ADDR       listen address (default :8080)
//	ULTRA_DEV_TOKENS static dev auth, "token=email,token2=email2" (required
//	                 until real OIDC lands in Phase 7)
//	ULTRA_MIGRATE    run migrations at startup (default true)
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

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envprovider"
	"github.com/aleksclark/ultralogical/envwork"
	"github.com/aleksclark/ultralogical/flowwork"
	ultrahttp "github.com/aleksclark/ultralogical/http"
	riverqueue "github.com/aleksclark/ultralogical/jobqueue/river"
	"github.com/aleksclark/ultralogical/postgres"
	"github.com/aleksclark/ultralogical/secrets"
)

func main() {
	log := slog.New(secrets.NewRedactingHandler(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(log); err != nil {
		log.Error("ultrad exited", "error", err)
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
	addr := os.Getenv("ULTRA_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	devTokens := ultra.ParseDevTokens(os.Getenv("ULTRA_DEV_TOKENS"))
	if len(devTokens) == 0 {
		return errors.New("ULTRA_DEV_TOKENS is required (no other authenticator is configured yet)")
	}
	keyring, err := secrets.NewAESKeyring(os.Getenv("ULTRA_MASTER_KEY"))
	if err != nil {
		return err
	}

	if os.Getenv("ULTRA_MIGRATE") != "false" {
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

	// Enqueue-only queue handle: ultrad inserts step jobs; workers run them.
	queue, err := riverqueue.New(ctx, pool, riverqueue.Config{})
	if err != nil {
		return err
	}

	envs := &envwork.Service{Store: store, Enqueue: postgres.TxEnqueuer{Queue: queue}, Keyring: keyring, Log: log}

	defaultModel := ultra.ModelConfig{
		Provider:   envOr("ULTRA_DEFAULT_PROVIDER", "openai"),
		ModelID:    envOr("ULTRA_DEFAULT_MODEL", "gpt-4.1-mini"),
		Credential: "default",
	}

	// ultrad only accepts and cancels invocations; workers advance them. The
	// service is shared so acceptance and orchestration cannot drift.
	flows := &flowwork.Service{
		Store: store, Enqueue: postgres.TxEnqueuer{Queue: queue}, Envs: envs,
		Log: log, DefaultModel: defaultModel,
	}

	// Registration probes the real control plane through this registry, so a
	// stored provider is one that answered rather than one that parsed.
	providers := envprovider.StandardRegistry(providerDeployment())
	handler := ultrahttp.NewHandler(ultrahttp.Config{
		Store:        store,
		Providers:    providers,
		Auth:         ultra.NewDevTokenAuthenticator(store, devTokens),
		Bus:          bus,
		Log:          log,
		Keyring:      keyring,
		Enqueue:      postgres.TxEnqueuer{Queue: queue},
		DefaultModel: defaultModel,
		Envs:         envs,
		Flows:        flows,
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
	log.Info("ultrad listening", "addr", addr)

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
		BezalelImage:           envOr("ULTRA_BEZALEL_IMAGE", "ultralogical/bezalel:local"),
		BezalelBinary:          os.Getenv("ULTRA_BEZALEL_BINARY"),
		KubernetesEndpointMode: envOr("ULTRA_K8S_ENDPOINT_MODE", ""),
		KubernetesEndpointHost: envOr("ULTRA_K8S_ENDPOINT_HOST", ""),
	}
	if kinds := os.Getenv("ULTRA_PROVIDER_KINDS"); kinds != "" {
		deployment.EnabledKinds = strings.Split(kinds, ",")
	}
	if cidrs := os.Getenv("ULTRA_HOSTED_INGRESS_CIDRS"); cidrs != "" {
		deployment.HostedIngressCIDRs = strings.Split(cidrs, ",")
	}
	if low, high := envInt32("ULTRA_K8S_NODEPORT_LOW"), envInt32("ULTRA_K8S_NODEPORT_HIGH"); high > 0 {
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
