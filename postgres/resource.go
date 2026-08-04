package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	uc "github.com/aleksclark/ultracore"
)

type resourceStore struct{ scope *tenantScope }
type providerStore struct{ scope *tenantScope }

// Schema still carries flow/rate_class columns until the E4 squash; readers
// discard them and writers leave defaults.
const resourceColumns = `id, org_id, session_id, provider_instance_id, kind, state, spec, handle,
endpoint, token_hash, token_enc, epoch, failure_message, created_by_run_id,
created_at, updated_at, ready_at, terminated_at`

func scanResource(row pgx.Row) (uc.Resource, error) {
	var r uc.Resource
	var spec, handle []byte
	var endpoint string
	err := row.Scan(&r.ID, &r.TenantID, &r.SessionID, &r.ProviderInstanceID, &r.Kind,
		&r.State, &spec, &handle, &endpoint, &r.TokenHash, &r.TokenEnc,
		&r.Epoch, &r.FailureMessage, &r.CreatedByRunID,
		&r.CreatedAt, &r.UpdatedAt, &r.ReadyAt, &r.TerminatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uc.Resource{}, uc.ErrNotFound
	}
	if err != nil {
		return uc.Resource{}, fmt.Errorf("postgres: scan resource: %w", err)
	}
	r.Spec = append(json.RawMessage(nil), spec...)
	r.Handle = append(json.RawMessage(nil), handle...)
	r.Endpoint = uc.ToolEndpoint(endpoint)
	return r, nil
}

func (s *resourceStore) Create(ctx context.Context, r uc.Resource) error {
	spec := r.Spec
	if len(spec) == 0 {
		spec = []byte(`{}`)
	}
	kind := string(r.Kind)
	if kind == "" {
		kind = string(uc.ResourceKindDevEnv)
	}
	var createdBy any
	if r.CreatedByRunID != nil {
		createdBy = string(*r.CreatedByRunID)
	}
	tag, err := s.scope.s.db().Exec(ctx,
		`INSERT INTO dev_envs (id, org_id, session_id, provider_instance_id, kind, spec,
		 token_hash, token_enc, created_by_run_id)
		 SELECT $1, se.org_id, se.id, pi.id, $4, $5, $6, $7, $8
		 FROM sessions se JOIN provider_instances pi ON pi.org_id = se.org_id
		 WHERE se.id = $2 AND se.org_id = $3 AND pi.id = $9`,
		string(r.ID), string(r.SessionID), string(s.scope.org), kind, []byte(spec),
		r.TokenHash, r.TokenEnc, createdBy, string(r.ProviderInstanceID))
	if isUniqueViolation(err) {
		return uc.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create resource: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return uc.ErrNotFound
	}
	return nil
}

func (s *resourceStore) Get(ctx context.Context, id uc.ResourceID) (uc.Resource, error) {
	return scanResource(s.scope.s.db().QueryRow(ctx,
		`SELECT `+resourceColumns+` FROM dev_envs WHERE id = $1 AND org_id = $2`,
		string(id), string(s.scope.org)))
}

func (s *resourceStore) GetForUpdate(ctx context.Context, id uc.ResourceID) (uc.Resource, error) {
	return scanResource(s.scope.s.db().QueryRow(ctx,
		`SELECT `+resourceColumns+` FROM dev_envs WHERE id = $1 AND org_id = $2 FOR UPDATE`,
		string(id), string(s.scope.org)))
}

func (s *resourceStore) List(ctx context.Context, session uc.SessionID, kinds ...uc.ResourceKind) ([]uc.Resource, error) {
	if len(kinds) == 0 {
		return s.list(ctx, `SELECT `+resourceColumns+` FROM dev_envs
			WHERE session_id = $1 AND org_id = $2 ORDER BY created_at`, string(session), string(s.scope.org))
	}
	kindStrs := make([]string, len(kinds))
	for i, k := range kinds {
		kindStrs[i] = string(k)
	}
	return s.list(ctx, `SELECT `+resourceColumns+` FROM dev_envs
		WHERE session_id = $1 AND org_id = $2 AND kind = ANY($3) ORDER BY created_at`,
		string(session), string(s.scope.org), kindStrs)
}

func (s *resourceStore) ListActive(ctx context.Context) ([]uc.Resource, error) {
	return s.list(ctx, `SELECT `+resourceColumns+` FROM dev_envs
		WHERE org_id = $1 AND state NOT IN ('terminated', 'failed') ORDER BY created_at`, string(s.scope.org))
}

func (s *resourceStore) list(ctx context.Context, sql string, args ...any) ([]uc.Resource, error) {
	rows, err := s.scope.s.db().Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list resources: %w", err)
	}
	defer rows.Close()
	var out []uc.Resource
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *resourceStore) update(ctx context.Context, id uc.ResourceID, sql string, args ...any) error {
	all := []any{string(id), string(s.scope.org)}
	all = append(all, args...)
	tag, err := s.scope.s.db().Exec(ctx, sql, all...)
	if err != nil {
		return fmt.Errorf("postgres: update resource: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return uc.ErrNotFound
	}
	return nil
}

func (s *resourceStore) SetProvisioning(ctx context.Context, id uc.ResourceID) error {
	return s.update(ctx, id, `UPDATE dev_envs SET state='provisioning', updated_at=now()
		WHERE id=$1 AND org_id=$2`)
}

func (s *resourceStore) SetHandle(ctx context.Context, id uc.ResourceID, handle json.RawMessage) error {
	h := handle
	if len(h) == 0 {
		h = []byte(`{}`)
	}
	return s.update(ctx, id, `UPDATE dev_envs SET handle=$3, updated_at=now()
		WHERE id=$1 AND org_id=$2`, []byte(h))
}

func (s *resourceStore) SetReady(ctx context.Context, id uc.ResourceID, handle json.RawMessage, endpoint uc.ToolEndpoint) error {
	h := handle
	if len(h) == 0 {
		h = []byte(`{}`)
	}
	return s.update(ctx, id, `UPDATE dev_envs SET state='ready', handle=$3,
		endpoint=$4, ready_at=COALESCE(ready_at, now()), failure_message='', updated_at=now()
		WHERE id=$1 AND org_id=$2`, []byte(h), string(endpoint))
}

func (s *resourceStore) SetFailed(ctx context.Context, id uc.ResourceID, message string) error {
	return s.update(ctx, id, `UPDATE dev_envs SET state='failed', failure_message=$3,
		terminated_at=now(), updated_at=now() WHERE id=$1 AND org_id=$2`, message)
}

// SetSuspended parks a resource whose host is unreachable. It deliberately
// leaves terminated_at unset and keeps the handle and endpoint: the resource
// still exists, so resuming is a transition back to ready rather than a new
// provisioning.
//
// Only a ready or already-suspended resource can be suspended. A terminal
// one that raced this update is left alone rather than resurrected, and that
// is a no-op rather than an error: losing a race is not a failure.
func (s *resourceStore) SetSuspended(ctx context.Context, id uc.ResourceID, message string) error {
	tag, err := s.scope.s.db().Exec(ctx,
		`UPDATE dev_envs SET state='suspended', failure_message=$3, updated_at=now()
		 WHERE id=$1 AND org_id=$2 AND state IN ('ready','suspended')`,
		string(id), string(s.scope.org), message)
	if err != nil {
		return fmt.Errorf("postgres: suspend resource: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, getErr := s.Get(ctx, id); getErr != nil {
			return getErr
		}
	}
	return nil
}

func (s *resourceStore) SetTerminating(ctx context.Context, id uc.ResourceID) error {
	return s.update(ctx, id, `UPDATE dev_envs SET state='terminating', updated_at=now()
		WHERE id=$1 AND org_id=$2`)
}

func (s *resourceStore) SetTerminated(ctx context.Context, id uc.ResourceID) error {
	return s.update(ctx, id, `UPDATE dev_envs SET state='terminated',
		terminated_at=now(), updated_at=now() WHERE id=$1 AND org_id=$2`)
}

func (s *resourceStore) RotateToken(ctx context.Context, id uc.ResourceID, hash, enc []byte) error {
	return s.update(ctx, id, `UPDATE dev_envs SET token_hash=$3, token_enc=$4,
		epoch=epoch+1, updated_at=now() WHERE id=$1 AND org_id=$2`, hash, enc)
}

func (s *providerStore) Create(ctx context.Context, p uc.ProviderInstance) error {
	config := p.Config
	if len(config) == 0 {
		config = []byte(`{}`)
	}
	capabilities, err := json.Marshal(p.Capabilities)
	if err != nil {
		return fmt.Errorf("postgres: encode provider capabilities: %w", err)
	}
	// rate_class column remains until E4 squash; write the historical default.
	_, err = s.scope.s.db().Exec(ctx,
		`INSERT INTO provider_instances (id, org_id, kind, name, config, rate_class, state, capabilities)
		VALUES ($1,$2,$3,$4,$5,'byo',$6,$7)`, string(p.ID), string(s.scope.org), p.Kind,
		p.Name, config, p.State, capabilities)
	if isUniqueViolation(err) {
		return uc.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create provider instance: %w", err)
	}
	return nil
}

const providerColumns = `id, org_id, kind, name, config, state, capabilities, last_healthy_at, created_at`

func scanProvider(row pgx.Row) (uc.ProviderInstance, error) {
	var p uc.ProviderInstance
	var capabilities []byte
	err := row.Scan(&p.ID, &p.TenantID, &p.Kind, &p.Name, &p.Config,
		&p.State, &capabilities, &p.LastHealthyAt, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uc.ProviderInstance{}, uc.ErrNotFound
	}
	if err != nil {
		return uc.ProviderInstance{}, fmt.Errorf("postgres: scan provider: %w", err)
	}
	if err := json.Unmarshal(capabilities, &p.Capabilities); err != nil {
		return uc.ProviderInstance{}, fmt.Errorf("postgres: decode provider capabilities: %w", err)
	}
	return p, nil
}

func (s *providerStore) Get(ctx context.Context, id uc.ProviderInstanceID) (uc.ProviderInstance, error) {
	return scanProvider(s.scope.s.db().QueryRow(ctx,
		`SELECT `+providerColumns+` FROM provider_instances WHERE id=$1 AND org_id=$2`,
		string(id), string(s.scope.org)))
}

func (s *providerStore) GetByName(ctx context.Context, name string) (uc.ProviderInstance, error) {
	return scanProvider(s.scope.s.db().QueryRow(ctx,
		`SELECT `+providerColumns+` FROM provider_instances WHERE name=$1 AND org_id=$2`,
		name, string(s.scope.org)))
}

func (s *providerStore) List(ctx context.Context) ([]uc.ProviderInstance, error) {
	rows, err := s.scope.s.db().Query(ctx,
		`SELECT `+providerColumns+` FROM provider_instances WHERE org_id=$1 ORDER BY name`, string(s.scope.org))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uc.ProviderInstance
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *providerStore) Delete(ctx context.Context, id uc.ProviderInstanceID) error {
	tag, err := s.scope.s.db().Exec(ctx, `DELETE FROM provider_instances pi
		WHERE pi.id=$1 AND pi.org_id=$2 AND NOT EXISTS (
		SELECT 1 FROM dev_envs e WHERE e.provider_instance_id=pi.id AND e.state NOT IN ('terminated','failed'))`,
		string(id), string(s.scope.org))
	if err != nil {
		return fmt.Errorf("postgres: delete provider: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return uc.ErrNotFound
	}
	return nil
}

func (s *providerStore) MarkHealthy(ctx context.Context, id uc.ProviderInstanceID) error {
	tag, err := s.scope.s.db().Exec(ctx, `UPDATE provider_instances SET last_healthy_at=now(), state='ready'
		WHERE id=$1 AND org_id=$2`, string(id), string(s.scope.org))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return uc.ErrNotFound
	}
	return nil
}
