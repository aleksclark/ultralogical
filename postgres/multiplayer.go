package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/jackc/pgx/v5"
)

type participantStore struct{ scope *orgScope }
type memoryStore struct{ scope *orgScope }
type waitStore struct{ scope *orgScope }

func (s *participantStore) Join(ctx context.Context, p ultra.Participant) (bool, error) {
	var changed bool
	err := s.scope.s.db().QueryRow(ctx, `INSERT INTO participants(session_id,kind,participant_id,display,state)
SELECT se.id,$2,$3,$4,'active' FROM sessions se WHERE se.id=$1 AND se.org_id=$5
ON CONFLICT(session_id,kind,participant_id) DO UPDATE SET state='active',display=EXCLUDED.display,last_seen_at=now(),left_at=NULL
RETURNING (xmax=0 OR participants.state<>'active')`, string(p.SessionID), string(p.Kind), p.ParticipantID, p.Display, string(s.scope.org)).Scan(&changed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ultra.ErrNotFound
	}
	return changed, err
}
func (s *participantStore) Heartbeat(ctx context.Context, session ultra.SessionID, kind ultra.ParticipantKind, id string) error {
	tag, err := s.scope.s.db().Exec(ctx, `UPDATE participants p SET last_seen_at=now() FROM sessions se WHERE p.session_id=se.id AND p.session_id=$1 AND se.org_id=$2 AND p.kind=$3 AND p.participant_id=$4 AND p.state='active'`, string(session), string(s.scope.org), string(kind), id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ultra.ErrNotFound
	}
	return nil
}
func (s *participantStore) Leave(ctx context.Context, session ultra.SessionID, kind ultra.ParticipantKind, id string) (bool, error) {
	tag, err := s.scope.s.db().Exec(ctx, `UPDATE participants p SET state='left',left_at=now() FROM sessions se WHERE p.session_id=se.id AND p.session_id=$1 AND se.org_id=$2 AND p.kind=$3 AND p.participant_id=$4 AND p.state<>'left'`, string(session), string(s.scope.org), string(kind), id)
	return tag.RowsAffected() > 0, err
}
func (s *participantStore) List(ctx context.Context, session ultra.SessionID) ([]ultra.Participant, error) {
	rows, err := s.scope.s.db().Query(ctx, `SELECT p.session_id,p.kind,p.participant_id,p.display,p.state,p.joined_at,p.last_seen_at,p.left_at FROM participants p JOIN sessions se ON se.id=p.session_id WHERE p.session_id=$1 AND se.org_id=$2 ORDER BY p.joined_at`, string(session), string(s.scope.org))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ultra.Participant
	for rows.Next() {
		var p ultra.Participant
		if err := rows.Scan(&p.SessionID, &p.Kind, &p.ParticipantID, &p.Display, &p.State, &p.JoinedAt, &p.LastSeenAt, &p.LeftAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *participantStore) ReapIdle(ctx context.Context, cutoff time.Time, limit int) ([]ultra.Participant, error) {
	rows, err := s.scope.s.db().Query(ctx, `WITH picked AS (SELECT p.session_id,p.kind,p.participant_id FROM participants p JOIN sessions se ON se.id=p.session_id WHERE se.org_id=$1 AND p.state='active' AND p.last_seen_at<$2 ORDER BY p.last_seen_at LIMIT $3 FOR UPDATE OF p SKIP LOCKED) UPDATE participants p SET state='idle' FROM picked x WHERE p.session_id=x.session_id AND p.kind=x.kind AND p.participant_id=x.participant_id RETURNING p.session_id,p.kind,p.participant_id,p.display,p.state,p.joined_at,p.last_seen_at,p.left_at`, string(s.scope.org), cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ultra.Participant
	for rows.Next() {
		var p ultra.Participant
		if err := rows.Scan(&p.SessionID, &p.Kind, &p.ParticipantID, &p.Display, &p.State, &p.JoinedAt, &p.LastSeenAt, &p.LeftAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *memoryStore) lock(ctx context.Context, session ultra.SessionID) error {
	_, err := s.scope.s.db().Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,683124))`, string(session))
	return err
}
func (s *memoryStore) Get(ctx context.Context, session ultra.SessionID, key string) (ultra.SessionMemoryEntry, error) {
	var e ultra.SessionMemoryEntry
	err := s.scope.s.db().QueryRow(ctx, `SELECT m.session_id,m.key,m.value,m.updated_by_type,m.updated_by_id,m.updated_at FROM session_memory m JOIN sessions se ON se.id=m.session_id WHERE m.session_id=$1 AND se.org_id=$2 AND m.key=$3`, string(session), string(s.scope.org), key).Scan(&e.SessionID, &e.Key, &e.Value, &e.UpdatedBy.Type, &e.UpdatedBy.ID, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ultra.ErrNotFound
	}
	return e, err
}
func (s *memoryStore) List(ctx context.Context, session ultra.SessionID) ([]ultra.SessionMemoryEntry, error) {
	rows, err := s.scope.s.db().Query(ctx, `SELECT m.session_id,m.key,m.value,m.updated_by_type,m.updated_by_id,m.updated_at FROM session_memory m JOIN sessions se ON se.id=m.session_id WHERE m.session_id=$1 AND se.org_id=$2 ORDER BY m.key`, string(session), string(s.scope.org))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ultra.SessionMemoryEntry
	for rows.Next() {
		var e ultra.SessionMemoryEntry
		if err := rows.Scan(&e.SessionID, &e.Key, &e.Value, &e.UpdatedBy.Type, &e.UpdatedBy.ID, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *memoryStore) Set(ctx context.Context, e ultra.SessionMemoryEntry) error {
	if len(e.Key) == 0 {
		return errors.New("memory key required")
	}
	if len(e.Value) > 65536 {
		return errors.New("memory value exceeds 64KiB")
	}
	if err := s.lock(ctx, e.SessionID); err != nil {
		return err
	}
	var exists bool
	var count int
	if err := s.scope.s.db().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM session_memory WHERE session_id=$1 AND key=$2),(SELECT count(*) FROM session_memory WHERE session_id=$1)`, string(e.SessionID), e.Key).Scan(&exists, &count); err != nil {
		return err
	}
	if !exists && count >= 200 {
		return errors.New("session memory limit reached")
	}
	tag, err := s.scope.s.db().Exec(ctx, `INSERT INTO session_memory(session_id,key,value,updated_by_type,updated_by_id) SELECT se.id,$2,$3,$4,$5 FROM sessions se WHERE se.id=$1 AND se.org_id=$6 ON CONFLICT(session_id,key) DO UPDATE SET value=EXCLUDED.value,updated_by_type=EXCLUDED.updated_by_type,updated_by_id=EXCLUDED.updated_by_id,updated_at=now()`, string(e.SessionID), e.Key, e.Value, string(e.UpdatedBy.Type), e.UpdatedBy.ID, string(s.scope.org))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ultra.ErrNotFound
	}
	return nil
}
func (s *memoryStore) Delete(ctx context.Context, session ultra.SessionID, key string) error {
	if err := s.lock(ctx, session); err != nil {
		return err
	}
	tag, err := s.scope.s.db().Exec(ctx, `DELETE FROM session_memory m USING sessions se WHERE m.session_id=se.id AND m.session_id=$1 AND se.org_id=$2 AND m.key=$3`, string(session), string(s.scope.org), key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ultra.ErrNotFound
	}
	return nil
}

const waitColumns = `w.id,w.parent_run_id,w.step_index,w.tool_call_id,w.kind,w.state,w.timeout_policy,w.deadline,w.result,w.resumed_at`

func scanWait(row pgx.Row) (ultra.RunWait, error) {
	var w ultra.RunWait
	err := row.Scan(&w.ID, &w.ParentRunID, &w.StepIndex, &w.ToolCallID, &w.Kind, &w.State, &w.TimeoutPolicy, &w.Deadline, &w.Result, &w.ResumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, ultra.ErrNotFound
	}
	return w, err
}

func (s *waitStore) scanWaits(rows pgx.Rows) ([]ultra.RunWait, error) {
	defer rows.Close()
	var out []ultra.RunWait
	for rows.Next() {
		w, err := scanWait(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *waitStore) Create(ctx context.Context, w ultra.RunWait, members []ultra.RunWaitMember) error {
	if w.Kind == "" {
		w.Kind = ultra.WaitKindWait
	}
	if w.TimeoutPolicy == "" {
		w.TimeoutPolicy = ultra.TimeoutPolicyResolve
	}
	tag, err := s.scope.s.db().Exec(ctx, `INSERT INTO run_waits(id,parent_run_id,step_index,tool_call_id,kind,timeout_policy,deadline)
SELECT $1,ar.id,$3,$4,$7,$8,$5 FROM agent_runs ar WHERE ar.id=$2 AND ar.org_id=$6 ON CONFLICT DO NOTHING`,
		w.ID, string(w.ParentRunID), w.StepIndex, w.ToolCallID, w.Deadline, string(s.scope.org), w.Kind, w.TimeoutPolicy)
	if isUniqueViolation(err) {
		// A parent may hold at most one open wait; a second is a bug, not a
		// retry, so surface it rather than silently dropping the wait.
		return ultra.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create wait: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// The insert was a no-op: either the parent is not in this org, or the
		// identical wait already exists (redelivery). Distinguish them.
		if _, getErr := s.Get(ctx, w.ID); getErr == nil {
			return nil
		}
		return ultra.ErrNotFound
	}
	for _, m := range members {
		if _, err := s.scope.s.db().Exec(ctx, `INSERT INTO run_wait_members(wait_id,run_id,ordinal) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, w.ID, string(m.RunID), m.Ordinal); err != nil {
			return fmt.Errorf("postgres: create wait member: %w", err)
		}
	}
	return nil
}

func (s *waitStore) Get(ctx context.Context, id string) (ultra.RunWait, error) {
	return scanWait(s.scope.s.db().QueryRow(ctx, `SELECT `+waitColumns+` FROM run_waits w JOIN agent_runs ar ON ar.id=w.parent_run_id WHERE w.id=$1 AND ar.org_id=$2`, id, string(s.scope.org)))
}

// GetForUpdate locks the wait row so a terminal child and the timeout sweeper
// serialize instead of both closing it.
func (s *waitStore) GetForUpdate(ctx context.Context, id string) (ultra.RunWait, error) {
	return scanWait(s.scope.s.db().QueryRow(ctx, `SELECT `+waitColumns+` FROM run_waits w JOIN agent_runs ar ON ar.id=w.parent_run_id WHERE w.id=$1 AND ar.org_id=$2 FOR UPDATE OF w`, id, string(s.scope.org)))
}

func (s *waitStore) ListOpenForChild(ctx context.Context, id ultra.RunID) ([]ultra.RunWait, error) {
	rows, err := s.scope.s.db().Query(ctx, `SELECT `+waitColumns+` FROM run_waits w JOIN run_wait_members m ON m.wait_id=w.id JOIN agent_runs ar ON ar.id=w.parent_run_id WHERE m.run_id=$1 AND ar.org_id=$2 AND w.state='open'`, string(id), string(s.scope.org))
	if err != nil {
		return nil, fmt.Errorf("postgres: list waits for child: %w", err)
	}
	return s.scanWaits(rows)
}

func (s *waitStore) ListOpenForParent(ctx context.Context, id ultra.RunID) ([]ultra.RunWait, error) {
	rows, err := s.scope.s.db().Query(ctx, `SELECT `+waitColumns+` FROM run_waits w JOIN agent_runs ar ON ar.id=w.parent_run_id WHERE w.parent_run_id=$1 AND ar.org_id=$2 AND w.state='open'`, string(id), string(s.scope.org))
	if err != nil {
		return nil, fmt.Errorf("postgres: list waits for parent: %w", err)
	}
	return s.scanWaits(rows)
}

// ClaimDue transitions due open waits to timed_out and returns them. Claiming
// is the state transition itself, so two sweepers racing the same deadline
// cannot both time out one wait, and a child completing concurrently either
// wins the row lock or finds the wait already closed.
func (s *waitStore) ListForParent(ctx context.Context, id ultra.RunID) ([]ultra.RunWait, error) {
	rows, err := s.scope.s.db().Query(ctx, `SELECT `+waitColumns+` FROM run_waits w JOIN agent_runs ar ON ar.id=w.parent_run_id WHERE w.parent_run_id=$1 AND ar.org_id=$2 ORDER BY w.step_index`, string(id), string(s.scope.org))
	if err != nil {
		return nil, fmt.Errorf("postgres: list waits for parent: %w", err)
	}
	return s.scanWaits(rows)
}

func (s *waitStore) ClaimDue(ctx context.Context, now time.Time, limit int) ([]ultra.RunWait, error) {
	rows, err := s.scope.s.db().Query(ctx, `WITH due AS (
  SELECT w.id FROM run_waits w JOIN agent_runs ar ON ar.id=w.parent_run_id
   WHERE ar.org_id=$1 AND w.state='open' AND w.deadline<=$2
   ORDER BY w.deadline LIMIT $3 FOR UPDATE OF w SKIP LOCKED)
UPDATE run_waits w SET state='timed_out',resolved_at=now() FROM due WHERE w.id=due.id
RETURNING `+waitColumns, string(s.scope.org), now, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: claim due waits: %w", err)
	}
	return s.scanWaits(rows)
}

func (s *waitStore) Members(ctx context.Context, id string) ([]ultra.RunWaitMember, error) {
	rows, err := s.scope.s.db().Query(ctx, `SELECT wait_id,run_id,ordinal FROM run_wait_members WHERE wait_id=$1 ORDER BY ordinal`, id)
	if err != nil {
		return nil, fmt.Errorf("postgres: list wait members: %w", err)
	}
	defer rows.Close()
	var out []ultra.RunWaitMember
	for rows.Next() {
		var m ultra.RunWaitMember
		if err := rows.Scan(&m.WaitID, &m.RunID, &m.Ordinal); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Close leaves the open state exactly once. The `state='open'` predicate is
// the exactly-once guarantee: a second caller affects no rows and is told so.
func (s *waitStore) Close(ctx context.Context, id, state string, result json.RawMessage) (bool, error) {
	tag, err := s.scope.s.db().Exec(ctx, `UPDATE run_waits w SET state=$2,result=$4,resolved_at=now() FROM agent_runs ar WHERE w.parent_run_id=ar.id AND w.id=$1 AND ar.org_id=$3 AND w.state='open'`, id, state, string(s.scope.org), result)
	if err != nil {
		return false, fmt.Errorf("postgres: close wait: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetResult stores a closed wait's aggregate result. Claiming a due wait is
// what closes it, so the timeout path writes its result here rather than in
// the same statement.
func (s *waitStore) SetResult(ctx context.Context, id string, result json.RawMessage) error {
	tag, err := s.scope.s.db().Exec(ctx, `UPDATE run_waits w SET result=$2 FROM agent_runs ar WHERE w.parent_run_id=ar.id AND w.id=$1 AND ar.org_id=$3`, id, result, string(s.scope.org))
	if err != nil {
		return fmt.Errorf("postgres: set wait result: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ultra.ErrNotFound
	}
	return nil
}

// MarkResumed records that the parent's next step was enqueued. The
// `resumed_at IS NULL` predicate makes parent resumption at-most-once even if
// two paths (last child and timeout sweeper) both reach a closed wait.
func (s *waitStore) MarkResumed(ctx context.Context, id string) (bool, error) {
	tag, err := s.scope.s.db().Exec(ctx, `UPDATE run_waits w SET resumed_at=now() FROM agent_runs ar WHERE w.parent_run_id=ar.id AND w.id=$1 AND ar.org_id=$2 AND w.resumed_at IS NULL`, id, string(s.scope.org))
	if err != nil {
		return false, fmt.Errorf("postgres: mark wait resumed: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *runStore) CountChildren(ctx context.Context, id ultra.RunID) (int, error) {
	var n int
	err := r.scope.s.db().QueryRow(ctx, `SELECT count(*) FROM agent_runs WHERE parent_run_id=$1 AND org_id=$2`, string(id), string(r.scope.org)).Scan(&n)
	return n, err
}

var _ = fmt.Sprintf
