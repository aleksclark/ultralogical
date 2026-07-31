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
	"syscall"
	"time"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envprovider/proxy"
	"github.com/aleksclark/ultralogical/envwork"
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

	providerKinds := map[string]func(context.Context, []byte) error{ultra.ProviderKindLocalDocker: func(context.Context, []byte) error { return nil }}
	for _, kind := range []string{ultra.ProviderKindBYOKubernetes, ultra.ProviderKindHostedEKS, ultra.ProviderKindBYONomad, ultra.ProviderKindTunnelLocal} {
		k := kind
		providerKinds[k] = func(ctx context.Context, raw []byte) error {
			p, err := proxy.New(raw, k, nil)
			if err != nil {
				return err
			}
			return p.Validate(ctx)
		}
	}
	handler := ultrahttp.NewHandler(ultrahttp.Config{
		Store:         store,
		ProviderKinds: providerKinds,
		Auth:          ultra.NewDevTokenAuthenticator(store, devTokens),
		Bus:           bus,
		Log:           log,
		Keyring:       keyring,
		Enqueue:       postgres.TxEnqueuer{Queue: queue},
		DefaultModel:  defaultModel,
		Envs:          envs,
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
