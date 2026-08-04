// coreadmin is the private operator admin API server. It is a separate binary
// from cored and never shares routes, auth, or generated clients with the
// consumer API.
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aleksclark/ultracore/admin/authz"
	"github.com/aleksclark/ultracore/admin/command"
	"github.com/aleksclark/ultracore/admin/query"
	adminstore "github.com/aleksclark/ultracore/admin/store"
	"github.com/aleksclark/ultracore/adminhttp"
	"github.com/aleksclark/ultracore/config"
	riverqueue "github.com/aleksclark/ultracore/jobqueue/river"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/secrets"
)

// BuildVersion is set via -ldflags at release time.
var BuildVersion = "dev"

//go:embed all:spa
var spaEmbed embed.FS

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
	tokens, err := authz.LoadTokens()
	if err != nil {
		return err
	}
	tokens.DevMode = devMode
	if !tokens.HasTokens() && !devMode {
		return errors.New("CORE_ADMIN_TOKEN or CORE_ADMIN_TOKENS is required outside CORE_ADMIN_DEV_MODE")
	}
	if !tokens.HasTokens() && devMode {
		log.Warn("coreadmin running in dev mode without operator tokens; any non-empty bearer is accepted as admin")
	}

	addr := config.String("CORE_ADMIN_ADDR", "127.0.0.1:8082")
	corsOrigin := os.Getenv("CORE_ADMIN_CORS_ORIGIN")
	cursorSecret := os.Getenv("CORE_ADMIN_CURSOR_SECRET")
	if cursorSecret == "" {
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

	queue, err := riverqueue.New(ctx, pool, riverqueue.Config{})
	if err != nil {
		return fmt.Errorf("river migrate: %w", err)
	}

	var keyring secrets.Keyring
	if mk := os.Getenv("CORE_MASTER_KEY"); mk != "" {
		kr, err := secrets.NewAESKeyring(mk)
		if err != nil {
			return fmt.Errorf("CORE_MASTER_KEY: %w", err)
		}
		keyring = kr
	}

	revealEnabled := config.Bool("CORE_ADMIN_REVEAL_ENABLED", false)
	engine := command.New(command.Deps{
		Pool:         pool,
		Store:        store,
		Enqueue:      postgres.TxEnqueuer{Queue: queue},
		River:        queue,
		Keyring:      keyring,
		BuildVersion: BuildVersion,
		Flags: command.Flags{
			RevealEnabled:               revealEnabled,
			TerminateEnabled:            config.Bool("CORE_ADMIN_ENABLE_TERMINATE", false),
			SuspendEnabled:              config.Bool("CORE_ADMIN_ENABLE_SUSPEND", false),
			DisconnectSubscriberEnabled: config.Bool("CORE_ADMIN_ENABLE_DISCONNECT_SUBSCRIBER", false),
		},
		Log:           log,
		RateLimit:     config.Int("CORE_ADMIN_CMD_RATE_LIMIT", 20),
		MaxConcurrent: config.Int("CORE_ADMIN_CMD_CONCURRENCY", 8),
	})

	adminStore := adminstore.NewAdminStore(pool, &query.Signer{Secret: []byte(cursorSecret)}, BuildVersion)
	var spaFS http.FileSystem
	if sub, err := fs.Sub(spaEmbed, "spa"); err == nil {
		if f, err := sub.Open("index.html"); err == nil {
			_ = f.Close()
			spaFS = http.FS(sub)
		}
	}
	handler := adminhttp.NewHandler(adminhttp.Config{
		Store:         adminStore,
		Tokens:        tokens,
		DevMode:       devMode,
		CORSOrigin:    corsOrigin,
		Log:           log,
		Engine:        engine,
		RevealEnabled: revealEnabled,
		SPA:           spaFS,
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
	log.Info("coreadmin listening", "addr", addr, "dev_mode", devMode, "reveal_enabled", revealEnabled)

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
