// coreadmin is the private operator admin API server. It is a separate binary
// from cored and never shares routes, auth, or generated clients with the
// consumer API.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aleksclark/ultracore/admin/query"
	"github.com/aleksclark/ultracore/adminhttp"
	"github.com/aleksclark/ultracore/config"
	riverqueue "github.com/aleksclark/ultracore/jobqueue/river"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/secrets"
)

// BuildVersion is set via -ldflags at release time.
var BuildVersion = "dev"

func main() {
	log := slog.New(secrets.NewRedactingHandler(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(log); err != nil {
		log.Error("coreadmin exited", "error", err)
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

	devMode := config.Bool("CORE_ADMIN_DEV_MODE", false)
	token := os.Getenv("CORE_ADMIN_TOKEN")
	if token == "" && !devMode {
		return errors.New("CORE_ADMIN_TOKEN is required outside CORE_ADMIN_DEV_MODE")
	}
	if token == "" && devMode {
		log.Warn("coreadmin running in dev mode without CORE_ADMIN_TOKEN; any non-empty bearer is accepted")
	}

	addr := config.String("CORE_ADMIN_ADDR", "127.0.0.1:8082")
	corsOrigin := os.Getenv("CORE_ADMIN_CORS_ORIGIN")
	cursorSecret := os.Getenv("CORE_ADMIN_CURSOR_SECRET")
	if cursorSecret == "" {
		// Derive an ephemeral secret so cursors work in local dev; production
		// should set CORE_ADMIN_CURSOR_SECRET for multi-replica stability.
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		cursorSecret = hex.EncodeToString(b)
		log.Info("CORE_ADMIN_CURSOR_SECRET unset; using ephemeral process secret")
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
	_ = store

	// Ensure River schema exists so job introspection works.
	if _, err := riverqueue.New(ctx, pool, riverqueue.Config{}); err != nil {
		return fmt.Errorf("river migrate: %w", err)
	}

	adminStore := postgres.NewAdminStore(pool, &query.Signer{Secret: []byte(cursorSecret)}, BuildVersion)
	handler := adminhttp.NewHandler(adminhttp.Config{
		Store:      adminStore,
		Token:      token,
		DevMode:    devMode,
		CORSOrigin: corsOrigin,
		Log:        log,
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
	log.Info("coreadmin listening", "addr", addr, "dev_mode", devMode)

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
