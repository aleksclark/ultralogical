package postgres

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"

	ultra "github.com/aleksclark/ultralogical"
)

type flowStore struct{ scope *orgScope }

const flowColumns = `id, org_id, name, version, definition, created_at`

func scanFlow(row pgx.Row) (ultra.Flow, error) {
	var f ultra.Flow
	err := row.Scan(&f.ID, &f.OrgID, &f.Name, &f.Version, &f.Definition, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ultra.Flow{}, ultra.ErrNotFound
	}
	if err != nil {
		return ultra.Flow{}, fmt.Errorf("postgres: scan flow: %w", err)
	}
	return f, nil
}

// flowLockKey derives the advisory-lock key that serializes version
// assignment for one (org, name). Two concurrent writers must not both read
// max(version) and then both insert that value plus one: one would fail on the
// unique index and the outcome of the race would be undocumented. Serializing
// makes the documented outcome — two distinct ascending versions, no
// definition overwritten — a database property.
func flowLockKey(org ultra.OrgID, name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(org))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64()) //nolint:gosec // advisory-lock keys are arbitrary bit patterns.
}

// Put stores an immutable version. An explicit version that already exists is
// rejected: a flow definition is never rewritten, because an invocation that
// pinned it must keep replaying the same work.
func (s *flowStore) Put(ctx context.Context, f ultra.Flow) (ultra.Flow, error) {
	if f.Version < 0 {
		return f, fmt.Errorf("postgres: put flow: negative version")
	}
	if f.Version > 0 {
		err := s.scope.s.db().QueryRow(ctx,
			`INSERT INTO flows(id,org_id,name,version,definition)
			 VALUES($1,$2,$3,$4,$5) RETURNING created_at`,
			string(f.ID), string(s.scope.org), f.Name, f.Version, f.Definition).Scan(&f.CreatedAt)
		if isUniqueViolation(err) {
			return f, ultra.ErrAlreadyExists
		}
		if err != nil {
			return f, fmt.Errorf("postgres: put flow: %w", err)
		}
		f.OrgID = s.scope.org
		return f, nil
	}
	// Auto-assign runs inside a transaction holding the per-name advisory
	// lock, so concurrent writers converge on distinct ascending versions.
	err := s.scope.s.Tx(ctx, func(txs ultra.Store) error {
		tx, ok := txs.(*Store)
		if !ok {
			return fmt.Errorf("postgres: put flow: unexpected store type %T", txs)
		}
		if _, err := tx.db().Exec(ctx, `SELECT pg_advisory_xact_lock($1)`,
			flowLockKey(s.scope.org, f.Name)); err != nil {
			return fmt.Errorf("postgres: lock flow name: %w", err)
		}
		var next int
		if err := tx.db().QueryRow(ctx,
			`SELECT COALESCE(max(version),0)+1 FROM flows WHERE org_id=$1 AND name=$2`,
			string(s.scope.org), f.Name).Scan(&next); err != nil {
			return fmt.Errorf("postgres: next flow version: %w", err)
		}
		f.Version = next
		if err := tx.db().QueryRow(ctx,
			`INSERT INTO flows(id,org_id,name,version,definition)
			 VALUES($1,$2,$3,$4,$5) RETURNING created_at`,
			string(f.ID), string(s.scope.org), f.Name, f.Version, f.Definition).Scan(&f.CreatedAt); err != nil {
			return fmt.Errorf("postgres: put flow: %w", err)
		}
		return nil
	})
	if err != nil {
		return f, err
	}
	f.OrgID = s.scope.org
	return f, nil
}

func (s *flowStore) Get(ctx context.Context, name string, version int) (ultra.Flow, error) {
	if version == 0 {
		return scanFlow(s.scope.s.db().QueryRow(ctx,
			`SELECT `+flowColumns+` FROM flows WHERE org_id=$1 AND name=$2
			 ORDER BY version DESC LIMIT 1`, string(s.scope.org), name))
	}
	return scanFlow(s.scope.s.db().QueryRow(ctx,
		`SELECT `+flowColumns+` FROM flows WHERE org_id=$1 AND name=$2 AND version=$3`,
		string(s.scope.org), name, version))
}

func (s *flowStore) GetByID(ctx context.Context, id ultra.FlowID) (ultra.Flow, error) {
	return scanFlow(s.scope.s.db().QueryRow(ctx,
		`SELECT `+flowColumns+` FROM flows WHERE id=$1 AND org_id=$2`,
		string(id), string(s.scope.org)))
}

func (s *flowStore) List(ctx context.Context) ([]ultra.Flow, error) {
	rows, err := s.scope.s.db().Query(ctx,
		`SELECT DISTINCT ON(name) `+flowColumns+` FROM flows WHERE org_id=$1
		 ORDER BY name, version DESC`, string(s.scope.org))
	if err != nil {
		return nil, fmt.Errorf("postgres: list flows: %w", err)
	}
	return scanFlows(rows)
}

func (s *flowStore) ListVersions(ctx context.Context, name string) ([]ultra.Flow, error) {
	rows, err := s.scope.s.db().Query(ctx,
		`SELECT `+flowColumns+` FROM flows WHERE org_id=$1 AND name=$2 ORDER BY version DESC`,
		string(s.scope.org), name)
	if err != nil {
		return nil, fmt.Errorf("postgres: list flow versions: %w", err)
	}
	return scanFlows(rows)
}

func scanFlows(rows pgx.Rows) ([]ultra.Flow, error) {
	defer rows.Close()
	var out []ultra.Flow
	for rows.Next() {
		f, err := scanFlow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

const invocationColumns = `id, org_id, session_id, flow_id, flow_name, flow_version, params,
rendered, state, terminal_reason, message, cancel_requested_at, advance_at, created_at, updated_at`

func scanInvocation(row pgx.Row) (ultra.FlowInvocation, error) {
	var inv ultra.FlowInvocation
	err := row.Scan(&inv.ID, &inv.OrgID, &inv.SessionID, &inv.FlowID, &inv.FlowName,
		&inv.FlowVersion, &inv.Params, &inv.Rendered, &inv.State, &inv.TerminalReason,
		&inv.Message, &inv.CancelRequestedAt, &inv.AdvanceAt, &inv.CreatedAt, &inv.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ultra.FlowInvocation{}, ultra.ErrNotFound
	}
	if err != nil {
		return ultra.FlowInvocation{}, fmt.Errorf("postgres: scan flow invocation: %w", err)
	}
	return inv, nil
}

func (s *flowStore) CreateInvocation(ctx context.Context, i ultra.FlowInvocation) error {
	params := i.Params
	if len(params) == 0 {
		params = []byte(`{}`)
	}
	rendered := i.Rendered
	if len(rendered) == 0 {
		rendered = []byte(`{}`)
	}
	state := i.State
	if state == "" {
		state = ultra.FlowInvocationPending
	}
	// Session and flow ownership are checked in the same statement: an
	// invocation can only attach to a session and a flow this org owns.
	tag, err := s.scope.s.db().Exec(ctx,
		`INSERT INTO flow_invocations
		 (id,org_id,session_id,flow_id,flow_name,flow_version,params,rendered,state,advance_at)
		 SELECT $1, se.org_id, se.id, f.id, $6, $7, $8, $9, $10, $11
		   FROM sessions se JOIN flows f ON f.org_id = se.org_id
		  WHERE se.id=$2 AND se.org_id=$3 AND f.id=$4 AND f.org_id=$5`,
		string(i.ID), string(i.SessionID), string(s.scope.org), string(i.FlowID),
		string(s.scope.org), i.FlowName, i.FlowVersion, params, rendered, string(state), i.AdvanceAt)
	if isUniqueViolation(err) {
		return ultra.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create flow invocation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ultra.ErrNotFound
	}
	return nil
}

func (s *flowStore) GetInvocation(ctx context.Context, id ultra.FlowInvocationID) (ultra.FlowInvocation, error) {
	return scanInvocation(s.scope.s.db().QueryRow(ctx,
		`SELECT `+invocationColumns+` FROM flow_invocations WHERE id=$1 AND org_id=$2`,
		string(id), string(s.scope.org)))
}

func (s *flowStore) GetInvocationForUpdate(ctx context.Context, id ultra.FlowInvocationID) (ultra.FlowInvocation, error) {
	return scanInvocation(s.scope.s.db().QueryRow(ctx,
		`SELECT `+invocationColumns+` FROM flow_invocations WHERE id=$1 AND org_id=$2 FOR UPDATE`,
		string(id), string(s.scope.org)))
}

func (s *flowStore) ListInvocations(ctx context.Context, session ultra.SessionID) ([]ultra.FlowInvocation, error) {
	rows, err := s.scope.s.db().Query(ctx,
		`SELECT `+invocationColumns+` FROM flow_invocations
		  WHERE org_id=$1 AND session_id=$2 ORDER BY created_at DESC`,
		string(s.scope.org), string(session))
	if err != nil {
		return nil, fmt.Errorf("postgres: list flow invocations: %w", err)
	}
	defer rows.Close()
	var out []ultra.FlowInvocation
	for rows.Next() {
		inv, err := scanInvocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// SetInvocationState transitions the invocation. A terminal invocation is
// never reopened: convergence has to be final, or a retried advance could
// resurrect finished work.
func (s *flowStore) SetInvocationState(ctx context.Context, id ultra.FlowInvocationID,
	state ultra.FlowInvocationState, terminalReason, message string) error {
	keepAdvance := !state.Terminal()
	tag, err := s.scope.s.db().Exec(ctx,
		`UPDATE flow_invocations
		    SET state=$3, terminal_reason=$4, message=$5, updated_at=now(),
		        advance_at = CASE WHEN $6 THEN advance_at ELSE NULL END
		  WHERE id=$1 AND org_id=$2
		    AND state NOT IN ('completed','failed','cancelled')`,
		string(id), string(s.scope.org), string(state), terminalReason, message, keepAdvance)
	if err != nil {
		return fmt.Errorf("postgres: set flow invocation state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either the row is not in this org, or it is already terminal. A
		// no-op on an already-terminal invocation is correct, so only a
		// missing row is an error.
		if _, getErr := s.GetInvocation(ctx, id); getErr != nil {
			return getErr
		}
	}
	return nil
}

func (s *flowStore) RequestInvocationCancel(ctx context.Context, id ultra.FlowInvocationID) error {
	tag, err := s.scope.s.db().Exec(ctx,
		`UPDATE flow_invocations
		    SET cancel_requested_at = COALESCE(cancel_requested_at, now()),
		        state = CASE WHEN state IN ('completed','failed','cancelled')
		                     THEN state ELSE 'cancelling' END,
		        advance_at = CASE WHEN state IN ('completed','failed','cancelled')
		                          THEN advance_at ELSE now() END,
		        updated_at = now()
		  WHERE id=$1 AND org_id=$2`,
		string(id), string(s.scope.org))
	if err != nil {
		return fmt.Errorf("postgres: cancel flow invocation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ultra.ErrNotFound
	}
	return nil
}

// ClaimAdvance reserves the next advance tick. Only a due, non-terminal
// invocation can be claimed, and claiming pushes the watermark forward, so two
// workers cannot drive the same invocation at the same moment, and an
// invocation whose worker died is re-driven when its watermark comes due.
func (s *flowStore) ClaimAdvance(ctx context.Context, id ultra.FlowInvocationID, next time.Time) (bool, error) {
	tag, err := s.scope.s.db().Exec(ctx,
		`UPDATE flow_invocations SET advance_at=$3, updated_at=now()
		  WHERE id=$1 AND org_id=$2
		    AND state NOT IN ('completed','failed','cancelled')
		    AND (advance_at IS NULL OR advance_at <= now())`,
		string(id), string(s.scope.org), next)
	if err != nil {
		return false, fmt.Errorf("postgres: claim flow advance: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// AppendProgress records one lifecycle step. Seq is assigned in the same
// statement so progress is gapless per invocation, and the unique key makes a
// redelivered advance a no-op instead of duplicated history.
func (s *flowStore) AppendProgress(ctx context.Context, p ultra.FlowInvocationProgress) (bool, error) {
	tag, err := s.scope.s.db().Exec(ctx,
		`INSERT INTO flow_invocation_progress(invocation_id,seq,stage,key,detail)
		 SELECT fi.id,
		        COALESCE((SELECT max(seq) FROM flow_invocation_progress p WHERE p.invocation_id=fi.id),0)+1,
		        $3, $4, $5
		   FROM flow_invocations fi WHERE fi.id=$1 AND fi.org_id=$2
		 ON CONFLICT (invocation_id,key) DO NOTHING`,
		string(p.InvocationID), string(s.scope.org), p.Stage, p.Key, p.Detail)
	if err != nil {
		return false, fmt.Errorf("postgres: append flow progress: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *flowStore) Progress(ctx context.Context, id ultra.FlowInvocationID) ([]ultra.FlowInvocationProgress, error) {
	rows, err := s.scope.s.db().Query(ctx,
		`SELECT p.invocation_id, p.seq, p.stage, p.key, p.detail, p.at
		   FROM flow_invocation_progress p
		   JOIN flow_invocations fi ON fi.id = p.invocation_id
		  WHERE p.invocation_id=$1 AND fi.org_id=$2 ORDER BY p.seq`,
		string(id), string(s.scope.org))
	if err != nil {
		return nil, fmt.Errorf("postgres: list flow progress: %w", err)
	}
	defer rows.Close()
	var out []ultra.FlowInvocationProgress
	for rows.Next() {
		var p ultra.FlowInvocationProgress
		if err := rows.Scan(&p.InvocationID, &p.Seq, &p.Stage, &p.Key, &p.Detail, &p.At); err != nil {
			return nil, fmt.Errorf("postgres: scan flow progress: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *flowStore) InvocationRuns(ctx context.Context, id ultra.FlowInvocationID) ([]ultra.AgentRun, error) {
	runs := &runStore{scope: s.scope}
	rows, err := s.scope.s.db().Query(ctx,
		`SELECT `+runColumns+` FROM agent_runs
		  WHERE flow_invocation_id=$1 AND org_id=$2 ORDER BY created_at, id`,
		string(id), string(s.scope.org))
	if err != nil {
		return nil, fmt.Errorf("postgres: list flow runs: %w", err)
	}
	defer rows.Close()
	var out []ultra.AgentRun
	for rows.Next() {
		run, err := runs.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *flowStore) InvocationEnvs(ctx context.Context, id ultra.FlowInvocationID) ([]ultra.DevEnv, error) {
	rows, err := s.scope.s.db().Query(ctx,
		`SELECT `+envColumns+` FROM dev_envs
		  WHERE flow_invocation_id=$1 AND org_id=$2 ORDER BY flow_env_name, created_at`,
		string(id), string(s.scope.org))
	if err != nil {
		return nil, fmt.Errorf("postgres: list flow envs: %w", err)
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
