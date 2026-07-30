package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	ultra "github.com/aleksclark/ultralogical"
)

type envStore struct{ scope *orgScope }
type providerStore struct{ scope *orgScope }
type usageStore struct{ scope *orgScope }

const envColumns = `id, org_id, session_id, provider_instance_id, state, spec, handle,
endpoint, token_hash, token_enc, epoch, failure_message, created_by_run_id,
created_at, updated_at, ready_at, terminated_at`

func scanEnv(row pgx.Row) (ultra.DevEnv, error) {
	var env ultra.DevEnv
	var spec, handle []byte
	err := row.Scan(&env.ID, &env.OrgID, &env.SessionID, &env.ProviderInstanceID,
		&env.State, &spec, &handle, &env.Endpoint, &env.TokenHash, &env.TokenEnc,
		&env.Epoch, &env.FailureMessage, &env.CreatedByRunID, &env.CreatedAt,
		&env.UpdatedAt, &env.ReadyAt, &env.TerminatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ultra.DevEnv{}, ultra.ErrNotFound
	}
	if err != nil {
		return ultra.DevEnv{}, fmt.Errorf("postgres: scan env: %w", err)
	}
	if err := json.Unmarshal(spec, &env.Spec); err != nil {
		return ultra.DevEnv{}, fmt.Errorf("postgres: decode env spec: %w", err)
	}
	if err := json.Unmarshal(handle, &env.Handle); err != nil {
		return ultra.DevEnv{}, fmt.Errorf("postgres: decode provider handle: %w", err)
	}
	return env, nil
}

func (s *envStore) Create(ctx context.Context, env ultra.DevEnv) error {
	spec, err := json.Marshal(env.Spec)
	if err != nil {
		return fmt.Errorf("postgres: encode env spec: %w", err)
	}
	var createdBy any
	if env.CreatedByRunID != nil {
		createdBy = string(*env.CreatedByRunID)
	}
	tag, err := s.scope.s.db().Exec(ctx,
		`INSERT INTO dev_envs (id, org_id, session_id, provider_instance_id, spec,
		 token_hash, token_enc, created_by_run_id)
		 SELECT $1, se.org_id, se.id, pi.id, $4, $5, $6, $7
		 FROM sessions se JOIN provider_instances pi ON pi.org_id = se.org_id
		 WHERE se.id = $2 AND se.org_id = $3 AND pi.id = $8`,
		string(env.ID), string(env.SessionID), string(s.scope.org), spec,
		env.TokenHash, env.TokenEnc, createdBy, string(env.ProviderInstanceID))
	if isUniqueViolation(err) {
		return ultra.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create env: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ultra.ErrNotFound
	}
	return nil
}

func (s *envStore) Get(ctx context.Context, id ultra.EnvID) (ultra.DevEnv, error) {
	return scanEnv(s.scope.s.db().QueryRow(ctx,
		`SELECT `+envColumns+` FROM dev_envs WHERE id = $1 AND org_id = $2`,
		string(id), string(s.scope.org)))
}

func (s *envStore) GetForUpdate(ctx context.Context, id ultra.EnvID) (ultra.DevEnv, error) {
	return scanEnv(s.scope.s.db().QueryRow(ctx,
		`SELECT `+envColumns+` FROM dev_envs WHERE id = $1 AND org_id = $2 FOR UPDATE`,
		string(id), string(s.scope.org)))
}

func (s *envStore) List(ctx context.Context, session ultra.SessionID) ([]ultra.DevEnv, error) {
	return s.list(ctx, `SELECT `+envColumns+` FROM dev_envs
		WHERE session_id = $1 AND org_id = $2 ORDER BY created_at`, string(session), string(s.scope.org))
}

func (s *envStore) ListActive(ctx context.Context) ([]ultra.DevEnv, error) {
	return s.list(ctx, `SELECT `+envColumns+` FROM dev_envs
		WHERE org_id = $1 AND state NOT IN ('terminated', 'failed') ORDER BY created_at`, string(s.scope.org))
}

func (s *envStore) list(ctx context.Context, sql string, args ...any) ([]ultra.DevEnv, error) {
	rows, err := s.scope.s.db().Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list envs: %w", err)
	}
	defer rows.Close()
	var out []ultra.DevEnv
	for rows.Next() {
		env, err := scanEnv(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, env)
	}
	return out, rows.Err()
}

func (s *envStore) update(ctx context.Context, id ultra.EnvID, sql string, args ...any) error {
	all := []any{string(id), string(s.scope.org)}
	all = append(all, args...)
	tag, err := s.scope.s.db().Exec(ctx, sql, all...)
	if err != nil {
		return fmt.Errorf("postgres: update env: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ultra.ErrNotFound
	}
	return nil
}

func (s *envStore) SetProvisioning(ctx context.Context, id ultra.EnvID) error {
	return s.update(ctx, id, `UPDATE dev_envs SET state='provisioning', updated_at=now()
		WHERE id=$1 AND org_id=$2`)
}

func (s *envStore) SetReady(ctx context.Context, id ultra.EnvID, handle ultra.ProviderHandle, endpoint string) error {
	h, err := json.Marshal(handle)
	if err != nil {
		return err
	}
	return s.update(ctx, id, `UPDATE dev_envs SET state='ready', handle=$3,
		endpoint=$4, ready_at=COALESCE(ready_at, now()), failure_message='', updated_at=now()
		WHERE id=$1 AND org_id=$2`, h, endpoint)
}

func (s *envStore) SetFailed(ctx context.Context, id ultra.EnvID, message string) error {
	return s.update(ctx, id, `UPDATE dev_envs SET state='failed', failure_message=$3,
		terminated_at=now(), updated_at=now() WHERE id=$1 AND org_id=$2`, message)
}

func (s *envStore) SetTerminating(ctx context.Context, id ultra.EnvID) error {
	return s.update(ctx, id, `UPDATE dev_envs SET state='terminating', updated_at=now()
		WHERE id=$1 AND org_id=$2`)
}

func (s *envStore) SetTerminated(ctx context.Context, id ultra.EnvID) error {
	return s.update(ctx, id, `UPDATE dev_envs SET state='terminated',
		terminated_at=now(), updated_at=now() WHERE id=$1 AND org_id=$2`)
}

func (s *envStore) RotateToken(ctx context.Context, id ultra.EnvID, hash, enc []byte) error {
	return s.update(ctx, id, `UPDATE dev_envs SET token_hash=$3, token_enc=$4,
		epoch=epoch+1, updated_at=now() WHERE id=$1 AND org_id=$2`, hash, enc)
}

func (s *providerStore) Create(ctx context.Context, p ultra.ProviderInstance) error {
	config := p.Config
	if len(config) == 0 {
		config = []byte(`{}`)
	}
	_, err := s.scope.s.db().Exec(ctx,
		`INSERT INTO provider_instances (id, org_id, kind, name, config, rate_class, state)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, string(p.ID), string(s.scope.org), p.Kind,
		p.Name, config, p.RateClass, p.State)
	if isUniqueViolation(err) {
		return ultra.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create provider instance: %w", err)
	}
	return nil
}

const providerColumns = `id, org_id, kind, name, config, rate_class, state, last_healthy_at, created_at`

func scanProvider(row pgx.Row) (ultra.ProviderInstance, error) {
	var p ultra.ProviderInstance
	err := row.Scan(&p.ID, &p.OrgID, &p.Kind, &p.Name, &p.Config, &p.RateClass,
		&p.State, &p.LastHealthyAt, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ultra.ProviderInstance{}, ultra.ErrNotFound
	}
	if err != nil {
		return ultra.ProviderInstance{}, fmt.Errorf("postgres: scan provider: %w", err)
	}
	return p, nil
}

func (s *providerStore) Get(ctx context.Context, id ultra.ProviderInstanceID) (ultra.ProviderInstance, error) {
	return scanProvider(s.scope.s.db().QueryRow(ctx,
		`SELECT `+providerColumns+` FROM provider_instances WHERE id=$1 AND org_id=$2`,
		string(id), string(s.scope.org)))
}

func (s *providerStore) GetByName(ctx context.Context, name string) (ultra.ProviderInstance, error) {
	return scanProvider(s.scope.s.db().QueryRow(ctx,
		`SELECT `+providerColumns+` FROM provider_instances WHERE name=$1 AND org_id=$2`,
		name, string(s.scope.org)))
}

func (s *providerStore) List(ctx context.Context) ([]ultra.ProviderInstance, error) {
	rows, err := s.scope.s.db().Query(ctx,
		`SELECT `+providerColumns+` FROM provider_instances WHERE org_id=$1 ORDER BY name`, string(s.scope.org))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ultra.ProviderInstance
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *providerStore) Delete(ctx context.Context, id ultra.ProviderInstanceID) error {
	tag, err := s.scope.s.db().Exec(ctx, `DELETE FROM provider_instances pi
		WHERE pi.id=$1 AND pi.org_id=$2 AND NOT EXISTS (
		SELECT 1 FROM dev_envs e WHERE e.provider_instance_id=pi.id AND e.state NOT IN ('terminated','failed'))`,
		string(id), string(s.scope.org))
	if err != nil {
		return fmt.Errorf("postgres: delete provider: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ultra.ErrNotFound
	}
	return nil
}

func (s *providerStore) MarkHealthy(ctx context.Context, id ultra.ProviderInstanceID) error {
	tag, err := s.scope.s.db().Exec(ctx, `UPDATE provider_instances SET last_healthy_at=now(), state='ready'
		WHERE id=$1 AND org_id=$2`, string(id), string(s.scope.org))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ultra.ErrNotFound
	}
	return nil
}

func (s *usageStore) Open(ctx context.Context, u ultra.EnvUsage) error {
	_, err := s.scope.s.db().Exec(ctx, `INSERT INTO env_usage
		(id, org_id, env_id, provider_instance_id, started_at, last_metered_at, rate_class)
		VALUES ($1,$2,$3,$4,$5,$5,$6) ON CONFLICT DO NOTHING`, u.ID, string(s.scope.org),
		string(u.EnvID), string(u.ProviderInstanceID), u.StartedAt, u.RateClass)
	if err != nil {
		return fmt.Errorf("postgres: open usage: %w", err)
	}
	return nil
}

func (s *usageStore) Tick(ctx context.Context, envID ultra.EnvID, at time.Time) error {
	_, err := s.scope.s.db().Exec(ctx, `UPDATE env_usage SET seconds=seconds+
		GREATEST(0, EXTRACT(EPOCH FROM ($3::timestamptz-last_metered_at)))::bigint,
		last_metered_at=$3 WHERE env_id=$1 AND org_id=$2 AND ended_at IS NULL`,
		string(envID), string(s.scope.org), at)
	return err
}

func (s *usageStore) Close(ctx context.Context, envID ultra.EnvID, at time.Time) error {
	_, err := s.scope.s.db().Exec(ctx, `UPDATE env_usage SET seconds=seconds+
		GREATEST(0, EXTRACT(EPOCH FROM ($3::timestamptz-last_metered_at)))::bigint,
		last_metered_at=$3, ended_at=$3 WHERE env_id=$1 AND org_id=$2 AND ended_at IS NULL`,
		string(envID), string(s.scope.org), at)
	return err
}

func (s *usageStore) List(ctx context.Context, from, to time.Time) ([]ultra.EnvUsage, error) {
	rows, err := s.scope.s.db().Query(ctx, `SELECT id, org_id, env_id, provider_instance_id,
		started_at, last_metered_at, ended_at, seconds, rate_class FROM env_usage
		WHERE org_id=$1 AND started_at < $3 AND COALESCE(ended_at, now()) >= $2 ORDER BY started_at`,
		string(s.scope.org), from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ultra.EnvUsage
	for rows.Next() {
		var u ultra.EnvUsage
		if err := rows.Scan(&u.ID, &u.OrgID, &u.EnvID, &u.ProviderInstanceID,
			&u.StartedAt, &u.LastMeteredAt, &u.EndedAt, &u.Seconds, &u.RateClass); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
