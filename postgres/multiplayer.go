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

func (s *waitStore) Create(ctx context.Context, w ultra.RunWait, members []ultra.RunWaitMember) error {
	_, err := s.scope.s.db().Exec(ctx, `INSERT INTO run_waits(id,parent_run_id,step_index,tool_call_id,deadline) SELECT $1,ar.id,$3,$4,$5 FROM agent_runs ar WHERE ar.id=$2 AND ar.org_id=$6 ON CONFLICT DO NOTHING`, w.ID, string(w.ParentRunID), w.StepIndex, w.ToolCallID, w.Deadline, string(s.scope.org))
	if err != nil {
		return err
	}
	for _, m := range members {
		if _, err := s.scope.s.db().Exec(ctx, `INSERT INTO run_wait_members(wait_id,run_id,ordinal) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, w.ID, string(m.RunID), m.Ordinal); err != nil {
			return err
		}
	}
	return nil
}
func (s *waitStore) ListOpenForChild(ctx context.Context, id ultra.RunID) ([]ultra.RunWait, error) {
	rows, err := s.scope.s.db().Query(ctx, `SELECT w.id,w.parent_run_id,w.step_index,w.tool_call_id,w.state,w.deadline,w.result FROM run_waits w JOIN run_wait_members m ON m.wait_id=w.id JOIN agent_runs ar ON ar.id=w.parent_run_id WHERE m.run_id=$1 AND ar.org_id=$2 AND w.state='open'`, string(id), string(s.scope.org))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ultra.RunWait
	for rows.Next() {
		var w ultra.RunWait
		if err := rows.Scan(&w.ID, &w.ParentRunID, &w.StepIndex, &w.ToolCallID, &w.State, &w.Deadline, &w.Result); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (s *waitStore) Members(ctx context.Context, id string) ([]ultra.RunWaitMember, error) {
	rows, err := s.scope.s.db().Query(ctx, `SELECT wait_id,run_id,ordinal FROM run_wait_members WHERE wait_id=$1 ORDER BY ordinal`, id)
	if err != nil {
		return nil, err
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
func (s *waitStore) Resolve(ctx context.Context, id string, result json.RawMessage) (bool, error) {
	tag, err := s.scope.s.db().Exec(ctx, `UPDATE run_waits w SET state='resolved',result=$2,resolved_at=now() FROM agent_runs ar WHERE w.parent_run_id=ar.id AND w.id=$1 AND ar.org_id=$3 AND w.state='open'`, id, result, string(s.scope.org))
	return tag.RowsAffected() > 0, err
}

func (r *runStore) CountChildren(ctx context.Context, id ultra.RunID) (int, error) {
	var n int
	err := r.scope.s.db().QueryRow(ctx, `SELECT count(*) FROM agent_runs WHERE parent_run_id=$1 AND org_id=$2`, string(id), string(r.scope.org)).Scan(&n)
	return n, err
}

var _ = fmt.Sprintf
