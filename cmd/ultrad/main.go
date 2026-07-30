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
	ultrahttp "github.com/aleksclark/ultralogical/http"
	"github.com/aleksclark/ultralogical/postgres"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
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

	handler := ultrahttp.NewHandler(ultrahttp.Config{
		Store: store,
		Auth:  ultra.NewDevTokenAuthenticator(store, devTokens),
		Bus:   bus,
		Log:   log,
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
