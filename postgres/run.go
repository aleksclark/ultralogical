package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	uc "github.com/aleksclark/ultracore"
)

type runStore struct{ scope *tenantScope }

const runColumns = `id, session_id, org_id, parent_run_id, COALESCE(spawn_key,''), COALESCE(cohort_id::text,''), COALESCE(cohort_ordinal,0),
	grants, result, state, loop_kind, loop_version, model_config,
	prompt, history, failure_reason, failure_message, cancel_requested_at, created_at, updated_at,
	COALESCE(actor_kind,''), COALESCE(actor_id,''), COALESCE(actor_display,'')`

func (r *runStore) scan(row pgx.Row) (uc.AgentRun, error) {
	var run uc.AgentRun
	var modelConfig, grants []byte
	err := row.Scan(&run.ID, &run.SessionID, &run.TenantID, &run.ParentRunID, &run.SpawnKey, &run.CohortID, &run.CohortOrdinal,
		&grants, &run.Result, &run.State, &run.LoopKind,
		&run.LoopVersion, &modelConfig, &run.Prompt, &run.History, &run.FailureReason,
		&run.FailureMessage, &run.CancelRequestedAt, &run.CreatedAt, &run.UpdatedAt,
		&run.Actor.Kind, &run.Actor.ID, &run.Actor.Display)
	if errors.Is(err, pgx.ErrNoRows) {
		return uc.AgentRun{}, uc.ErrNotFound
	}
	if err != nil {
		return uc.AgentRun{}, fmt.Errorf("postgres: scan run: %w", err)
	}
	if err := json.Unmarshal(modelConfig, &run.ModelConfig); err != nil {
		return uc.AgentRun{}, fmt.Errorf("postgres: decode model config: %w", err)
	}
	if err := decodePolicy(grants, &run.Policy); err != nil {
		return uc.AgentRun{}, fmt.Errorf("postgres: decode policy: %w", err)
	}
	return run, nil
}

// decodePolicy accepts both the E3 RunPolicy shape and the E1 interim
// {"tools":[...]} allowlist so existing rows and in-flight tests keep working
// until every writer uses the new shape.
func decodePolicy(raw []byte, p *uc.RunPolicy) error {
	if len(raw) == 0 || string(raw) == "null" {
		*p = uc.RunPolicy{}
		return nil
	}
	if err := json.Unmarshal(raw, p); err != nil {
		return err
	}
	// Legacy shape: only "tools" was set.
	var legacy struct {
		Tools []string `json:"tools"`
	}
	_ = json.Unmarshal(raw, &legacy)
	if len(p.AllowTools) == 0 && len(legacy.Tools) > 0 {
		p.AllowTools = legacy.Tools
	}
	return nil
}

func (r *runStore) Create(ctx context.Context, run uc.AgentRun) error {
	modelConfig, err := json.Marshal(run.ModelConfig)
	if err != nil {
		return fmt.Errorf("postgres: encode model config: %w", err)
	}
	grants, err := json.Marshal(run.Policy)
	if err != nil {
		return err
	}
	// A run with no grants is a run that may do nothing. Substituting root
	// authority here would silently escalate exactly the case that matters:
	// a child deliberately spawned with an empty tool list.
	history := run.History
	if len(history) == 0 {
		history = []byte(`{"v":1,"messages":[]}`)
	}
	// Session ownership is enforced in the same statement: the insert only
	// succeeds if the session belongs to this scope's org.
	var spawnKey, cohortID any
	if run.SpawnKey != "" {
		spawnKey = run.SpawnKey
	}
	if run.CohortID != "" {
		cohortID = run.CohortID
	}
	tag, err := r.scope.s.db().Exec(ctx,
		`INSERT INTO agent_runs (id, session_id, org_id, parent_run_id, grants, state, loop_kind, loop_version, model_config, prompt, history, spawn_key, cohort_id, cohort_ordinal, actor_kind, actor_id, actor_display)
		 SELECT $1, s.id, s.org_id, $3, $4, $5, $6, $7, $8, $9, $10, $12, $13, $14, $15, $16, $17
		   FROM sessions s WHERE s.id = $2 AND s.org_id = $11`,
		string(run.ID), string(run.SessionID), run.ParentRunID, grants, string(uc.RunPending), run.LoopKind,
		run.LoopVersion, modelConfig, run.Prompt, history, string(r.scope.org), spawnKey, cohortID, run.CohortOrdinal,
		run.Actor.Kind, run.Actor.ID, run.Actor.Display)
	if isUniqueViolation(err) {
		return uc.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return uc.ErrNotFound
	}
	return nil
}

func (r *runStore) Get(ctx context.Context, id uc.RunID) (uc.AgentRun, error) {
	return r.scan(r.scope.s.db().QueryRow(ctx,
		`SELECT `+runColumns+` FROM agent_runs WHERE id = $1 AND org_id = $2`,
		string(id), string(r.scope.org)))
}

func (r *runStore) GetForUpdate(ctx context.Context, id uc.RunID) (uc.AgentRun, error) {
	return r.scan(r.scope.s.db().QueryRow(ctx,
		`SELECT `+runColumns+` FROM agent_runs WHERE id = $1 AND org_id = $2 FOR UPDATE`,
		string(id), string(r.scope.org)))
}

func (r *runStore) List(ctx context.Context, session uc.SessionID) ([]uc.AgentRun, error) {
	rows, err := r.scope.s.db().Query(ctx,
		`SELECT `+runColumns+` FROM agent_runs
		  WHERE session_id = $1 AND org_id = $2 ORDER BY created_at`,
		string(session), string(r.scope.org))
	if err != nil {
		return nil, fmt.Errorf("postgres: list runs: %w", err)
	}
	defer rows.Close()
	var runs []uc.AgentRun
	for rows.Next() {
		run, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (r *runStore) SetHistory(ctx context.Context, id uc.RunID, history json.RawMessage) error {
	return r.exec(ctx,
		`UPDATE agent_runs SET history = $3, updated_at = now() WHERE id = $1 AND org_id = $2`,
		string(id), string(r.scope.org), history)
}

func (r *runStore) SetState(ctx context.Context, id uc.RunID, state uc.RunState, failureReason, failureMessage string) error {
	return r.exec(ctx,
		`UPDATE agent_runs SET state = $3, failure_reason = $4, failure_message = $5, updated_at = now()
		  WHERE id = $1 AND org_id = $2`,
		string(id), string(r.scope.org), string(state), failureReason, failureMessage)
}

// SetResult persists a run's final result. It is written in the same
// transaction that marks the run terminal, so a parent reading a terminal
// child always sees the result that child produced.
func (r *runStore) SetResult(ctx context.Context, id uc.RunID, result json.RawMessage) error {
	return r.exec(ctx,
		`UPDATE agent_runs SET result = $3, updated_at = now() WHERE id = $1 AND org_id = $2`,
		string(id), string(r.scope.org), result)
}

// GetBySpawnKey is the read half of spawn idempotency: a redelivered step
// replaying the same tool call finds the child it already created.
func (r *runStore) GetBySpawnKey(ctx context.Context, key string) (uc.AgentRun, error) {
	return r.scan(r.scope.s.db().QueryRow(ctx,
		`SELECT `+runColumns+` FROM agent_runs WHERE spawn_key = $1 AND org_id = $2`,
		key, string(r.scope.org)))
}

// Children lists direct children in creation order, which is the order clients
// render a run tree in.
func (r *runStore) Children(ctx context.Context, id uc.RunID) ([]uc.AgentRun, error) {
	rows, err := r.scope.s.db().Query(ctx,
		`SELECT `+runColumns+` FROM agent_runs
		  WHERE parent_run_id = $1 AND org_id = $2
		  ORDER BY COALESCE(cohort_ordinal, 0), created_at`,
		string(id), string(r.scope.org))
	if err != nil {
		return nil, fmt.Errorf("postgres: list children: %w", err)
	}
	defer rows.Close()
	var runs []uc.AgentRun
	for rows.Next() {
		run, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (r *runStore) RequestCancel(ctx context.Context, id uc.RunID) error {
	return r.exec(ctx,
		`UPDATE agent_runs SET cancel_requested_at = COALESCE(cancel_requested_at, now()), updated_at = now()
		  WHERE id = $1 AND org_id = $2`,
		string(id), string(r.scope.org))
}

func (r *runStore) exec(ctx context.Context, sql string, args ...any) error {
	tag, err := r.scope.s.db().Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("postgres: run update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return uc.ErrNotFound
	}
	return nil
}

func (r *runStore) InsertStep(ctx context.Context, s uc.RunStep) error {
	_, err := r.scope.s.db().Exec(ctx,
		`INSERT INTO agent_run_steps (agent_run_id, step_index, attempt, tokens_in, tokens_out, finish_reason)
		 SELECT $1, $2, $3, $4, $5, $6
		   FROM agent_runs WHERE id = $1 AND org_id = $7`,
		string(s.RunID), s.StepIndex, s.Attempt, s.TokensIn, s.TokensOut, s.FinishReason,
		string(r.scope.org))
	if isUniqueViolation(err) {
		return uc.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: insert step: %w", err)
	}
	return nil
}

func (r *runStore) Steps(ctx context.Context, id uc.RunID) ([]uc.RunStep, error) {
	rows, err := r.scope.s.db().Query(ctx,
		`SELECT st.agent_run_id, st.step_index, st.attempt, st.tokens_in, st.tokens_out, st.finish_reason, st.created_at
		   FROM agent_run_steps st JOIN agent_runs ar ON ar.id = st.agent_run_id
		  WHERE st.agent_run_id = $1 AND ar.org_id = $2 ORDER BY st.step_index`,
		string(id), string(r.scope.org))
	if err != nil {
		return nil, fmt.Errorf("postgres: list steps: %w", err)
	}
	defer rows.Close()
	var steps []uc.RunStep
	for rows.Next() {
		var s uc.RunStep
		if err := rows.Scan(&s.RunID, &s.StepIndex, &s.Attempt, &s.TokensIn, &s.TokensOut, &s.FinishReason, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan step: %w", err)
		}
		steps = append(steps, s)
	}
	return steps, rows.Err()
}

type credentialStore struct{ scope *tenantScope }

func (c *credentialStore) Put(ctx context.Context, cred uc.Credential) error {
	_, err := c.scope.s.db().Exec(ctx,
		`INSERT INTO credentials (org_id, kind, name, enc_payload)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (org_id, kind, name)
		 DO UPDATE SET enc_payload = EXCLUDED.enc_payload, rotated_at = now()`,
		string(c.scope.org), cred.Kind, cred.Name, cred.EncPayload)
	if err != nil {
		return fmt.Errorf("postgres: put credential: %w", err)
	}
	return nil
}

func (c *credentialStore) List(ctx context.Context) ([]uc.CredentialInfo, error) {
	rows, err := c.scope.s.db().Query(ctx,
		`SELECT kind, name, created_at, rotated_at FROM credentials
		  WHERE org_id = $1 ORDER BY kind, name`, string(c.scope.org))
	if err != nil {
		return nil, fmt.Errorf("postgres: list credentials: %w", err)
	}
	defer rows.Close()
	var infos []uc.CredentialInfo
	for rows.Next() {
		var info uc.CredentialInfo
		if err := rows.Scan(&info.Kind, &info.Name, &info.CreatedAt, &info.RotatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan credential: %w", err)
		}
		infos = append(infos, info)
	}
	return infos, rows.Err()
}

func (c *credentialStore) Get(ctx context.Context, kind, name string) (uc.Credential, error) {
	var cred uc.Credential
	err := c.scope.s.db().QueryRow(ctx,
		`SELECT org_id, kind, name, enc_payload, created_at, rotated_at FROM credentials
		  WHERE org_id = $1 AND kind = $2 AND name = $3`,
		string(c.scope.org), kind, name).
		Scan(&cred.TenantID, &cred.Kind, &cred.Name, &cred.EncPayload, &cred.CreatedAt, &cred.RotatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uc.Credential{}, uc.ErrNotFound
	}
	if err != nil {
		return uc.Credential{}, fmt.Errorf("postgres: get credential: %w", err)
	}
	return cred, nil
}

func (c *credentialStore) Delete(ctx context.Context, kind, name string) error {
	tag, err := c.scope.s.db().Exec(ctx,
		`DELETE FROM credentials WHERE org_id = $1 AND kind = $2 AND name = $3`,
		string(c.scope.org), kind, name)
	if err != nil {
		return fmt.Errorf("postgres: delete credential: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return uc.ErrNotFound
	}
	return nil
}
