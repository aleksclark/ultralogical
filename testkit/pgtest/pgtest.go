// Package pgtest provides a real PostgreSQL instance for tests via
// testcontainers. There are no database mocks anywhere in this codebase;
// store-dependent tests run against the same engine production uses.
package pgtest

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var (
	mu       sync.Mutex
	baseURL  string
	basePool *pgxpool.Pool
)

// serverURL starts (once per test process) a shared Postgres container and
// returns its admin URL.
func serverURL(t *testing.T) string {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if baseURL != "" {
		return baseURL
	}
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
		tcpostgres.WithSQLDriver("pgx"),
	)
	if err != nil {
		t.Fatalf("pgtest: start postgres: %v", err)
	}
	// The container is shared across the whole test process and reaped by
	// testcontainers' ryuk when the process exits.
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("pgtest: connection string: %v", err)
	}
	baseURL = url
	basePool, err = pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Fatalf("pgtest: admin pool: %v", err)
	}
	return baseURL
}

// NewDB creates a fresh database on the shared server and returns its URL.
// Each test gets full isolation without paying container startup per test.
func NewDB(t *testing.T) string {
	t.Helper()
	adminURL := serverURL(t)
	ctx := context.Background()
	name := "t_" + uuid.NewString()[:8]
	mu.Lock()
	_, err := basePool.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, name))
	mu.Unlock()
	if err != nil {
		t.Fatalf("pgtest: create database: %v", err)
	}
	url, err := replaceDBName(adminURL, name)
	if err != nil {
		t.Fatalf("pgtest: %v", err)
	}
	return url
}

// NewPool creates a fresh database and returns a connected pool, closed on
// test cleanup.
func NewPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	url := NewDB(t)
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pgtest: pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, url
}

// replaceDBName swaps the database path segment of a postgres URL.
func replaceDBName(rawURL, name string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}
