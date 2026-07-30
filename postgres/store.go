// Package postgres implements the ultra.Store interfaces on PostgreSQL using
// pgx. It is the only production store implementation; tests run against a
// real database, never a mock.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"

	ultra "github.com/aleksclark/ultralogical"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all pending migrations to the database at databaseURL.
func Migrate(ctx context.Context, databaseURL string) error {
	provider, err := gooseProvider(databaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = provider.Close() }()
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("postgres: migrate: %w", err)
	}
	return nil
}

func gooseProvider(databaseURL string) (*goose.Provider, error) {
	db, err := goose.OpenDBWithDriver("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: open for migrate: %w", err)
	}
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("postgres: migrations fs: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub)
	if err != nil {
		return nil, fmt.Errorf("postgres: goose provider: %w", err)
	}
	return provider, nil
}

// Store implements ultra.Store on a pgx connection pool.
type Store struct {
	pool *pgxpool.Pool
	tx   pgx.Tx // non-nil when transaction-bound
}

// NewStore connects a Store to the given pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Connect opens a pool and returns a Store backed by it.
func Connect(ctx context.Context, databaseURL string) (*Store, *pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return NewStore(pool), pool, nil
}

// db is the subset of pgx shared by pools and transactions.
type db interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Store) db() db {
	if s.tx != nil {
		return s.tx
	}
	return s.pool
}

// Orgs implements ultra.Store.
func (s *Store) Orgs() ultra.OrgStore { return &orgStore{s} }

// Users implements ultra.Store.
func (s *Store) Users() ultra.UserStore { return &userStore{s} }

// Org implements ultra.Store.
func (s *Store) Org(id ultra.OrgID) ultra.OrgScope { return &orgScope{s: s, org: id} }

// SessionOrg implements ultra.Store.
func (s *Store) SessionOrg(ctx context.Context, id ultra.SessionID) (ultra.OrgID, error) {
	var org ultra.OrgID
	err := s.db().QueryRow(ctx, `SELECT org_id FROM sessions WHERE id = $1`, string(id)).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ultra.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("postgres: session org: %w", err)
	}
	return org, nil
}

// Tx implements ultra.Store. Nested calls reuse the outer transaction.
func (s *Store) Tx(ctx context.Context, fn func(ultra.Store) error) error {
	if s.tx != nil {
		return fn(s)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op
	if err := fn(&Store{pool: s.pool, tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}
	return nil
}

// PgxTx exposes the bound transaction for transactional job enqueue. It
// returns nil when the store is not transaction-bound.
func (s *Store) PgxTx() pgx.Tx { return s.tx }

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
