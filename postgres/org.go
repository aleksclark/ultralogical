package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	uc "github.com/aleksclark/ultracore"
)

type tenantStore struct{ s *Store }

func (o *tenantStore) Create(ctx context.Context, tenant uc.Tenant) error {
	_, err := o.s.db().Exec(ctx,
		`INSERT INTO orgs (id, name) VALUES ($1, $2)`,
		string(tenant.ID), tenant.Name)
	if isUniqueViolation(err) {
		return uc.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create tenant: %w", err)
	}
	return nil
}

func (o *tenantStore) Get(ctx context.Context, id uc.TenantID) (uc.Tenant, error) {
	var tenant uc.Tenant
	err := o.s.db().QueryRow(ctx,
		`SELECT id, name, created_at FROM orgs WHERE id = $1`, string(id)).
		Scan(&tenant.ID, &tenant.Name, &tenant.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uc.Tenant{}, uc.ErrNotFound
	}
	if err != nil {
		return uc.Tenant{}, fmt.Errorf("postgres: get tenant: %w", err)
	}
	return tenant, nil
}

func (o *tenantStore) List(ctx context.Context) ([]uc.Tenant, error) {
	rows, err := o.s.db().Query(ctx,
		`SELECT id, name, created_at FROM orgs ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list tenants: %w", err)
	}
	defer rows.Close()
	var out []uc.Tenant
	for rows.Next() {
		var t uc.Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan tenant: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type apiKeyStore struct{ s *Store }

func (k *apiKeyStore) Create(ctx context.Context, key uc.APIKey) error {
	_, err := k.s.db().Exec(ctx,
		`INSERT INTO api_keys (id, org_id, name, scope, prefix, key_hash, key_enc)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		string(key.ID), string(key.TenantID), key.Name, string(key.Scope),
		key.Prefix, key.KeyHash, key.KeyEnc)
	if isUniqueViolation(err) {
		return uc.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create api key: %w", err)
	}
	return nil
}

func (k *apiKeyStore) scan(row pgx.Row) (uc.APIKey, error) {
	var key uc.APIKey
	var scope string
	err := row.Scan(&key.ID, &key.TenantID, &key.Name, &scope, &key.Prefix,
		&key.KeyHash, &key.KeyEnc, &key.CreatedAt, &key.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uc.APIKey{}, uc.ErrNotFound
	}
	if err != nil {
		return uc.APIKey{}, fmt.Errorf("postgres: scan api key: %w", err)
	}
	key.Scope = uc.KeyScope(scope)
	return key, nil
}

func (k *apiKeyStore) GetByHash(ctx context.Context, hash []byte) (uc.APIKey, error) {
	return k.scan(k.s.db().QueryRow(ctx,
		`SELECT id, org_id, name, scope, prefix, key_hash, key_enc, created_at, revoked_at
		   FROM api_keys WHERE key_hash = $1`, hash))
}

func (k *apiKeyStore) Get(ctx context.Context, id uc.APIKeyID) (uc.APIKey, error) {
	return k.scan(k.s.db().QueryRow(ctx,
		`SELECT id, org_id, name, scope, prefix, key_hash, key_enc, created_at, revoked_at
		   FROM api_keys WHERE id = $1`, string(id)))
}

func (k *apiKeyStore) List(ctx context.Context, tenant uc.TenantID) ([]uc.APIKeyInfo, error) {
	rows, err := k.s.db().Query(ctx,
		`SELECT id, org_id, name, scope, prefix, created_at, revoked_at
		   FROM api_keys WHERE org_id = $1 ORDER BY created_at`, string(tenant))
	if err != nil {
		return nil, fmt.Errorf("postgres: list api keys: %w", err)
	}
	defer rows.Close()
	var out []uc.APIKeyInfo
	for rows.Next() {
		var info uc.APIKeyInfo
		var scope string
		if err := rows.Scan(&info.ID, &info.TenantID, &info.Name, &scope,
			&info.Prefix, &info.CreatedAt, &info.RevokedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan api key info: %w", err)
		}
		info.Scope = uc.KeyScope(scope)
		out = append(out, info)
	}
	return out, rows.Err()
}

func (k *apiKeyStore) Revoke(ctx context.Context, tenant uc.TenantID, id uc.APIKeyID) error {
	tag, err := k.s.db().Exec(ctx,
		`UPDATE api_keys SET revoked_at = COALESCE(revoked_at, $3)
		  WHERE id = $1 AND org_id = $2`,
		string(id), string(tenant), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("postgres: revoke api key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return uc.ErrNotFound
	}
	return nil
}
