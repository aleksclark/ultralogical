package postgres

import (
	"context"
	"errors"
	"time"

	uc "github.com/aleksclark/ultracore"
	"github.com/jackc/pgx/v5"
)

type periodicStore struct{ scope *orgScope }

func (s *periodicStore) Create(ctx context.Context, p uc.PeriodicPrompt) error {
	_, err := s.scope.s.db().Exec(ctx, `INSERT INTO periodic_prompts(id,org_id,session_id,run_id,schedule,prompt,enabled,next_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, string(p.ID), string(s.scope.org), string(p.SessionID), nil, p.Schedule.String(), p.Prompt, p.Enabled, p.NextAt)
	return err
}
func scanPeriodic(row pgx.Row) (uc.PeriodicPrompt, error) {
	var p uc.PeriodicPrompt
	var schedule string
	var runID *string
	err := row.Scan(&p.ID, &p.OrgID, &p.SessionID, &runID, &schedule, &p.Prompt, &p.Enabled, &p.NextAt, &p.CreatedAt)
	if runID != nil {
		p.RunID = uc.RunID(*runID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return p, uc.ErrNotFound
	}
	p.Schedule, _ = time.ParseDuration(schedule)
	return p, err
}
func (s *periodicStore) List(ctx context.Context, session uc.SessionID) ([]uc.PeriodicPrompt, error) {
	rows, err := s.scope.s.db().Query(ctx, `SELECT id,org_id,session_id,run_id,schedule,prompt,enabled,next_at,created_at FROM periodic_prompts WHERE org_id=$1 AND session_id=$2 ORDER BY created_at`, string(s.scope.org), string(session))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uc.PeriodicPrompt
	for rows.Next() {
		p, e := scanPeriodic(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *periodicStore) GetForUpdate(ctx context.Context, id uc.PeriodicPromptID) (uc.PeriodicPrompt, error) {
	return scanPeriodic(s.scope.s.db().QueryRow(ctx, `SELECT id,org_id,session_id,run_id,schedule,prompt,enabled,next_at,created_at FROM periodic_prompts WHERE id=$1 AND org_id=$2 FOR UPDATE`, string(id), string(s.scope.org)))
}
func (s *periodicStore) SetEnabled(ctx context.Context, id uc.PeriodicPromptID, v bool) error {
	_, err := s.scope.s.db().Exec(ctx, `UPDATE periodic_prompts SET enabled=$3 WHERE id=$1 AND org_id=$2`, string(id), string(s.scope.org), v)
	return err
}
func (s *periodicStore) SetNext(ctx context.Context, id uc.PeriodicPromptID, next time.Time) error {
	_, err := s.scope.s.db().Exec(ctx, `UPDATE periodic_prompts SET next_at=$3 WHERE id=$1 AND org_id=$2`, string(id), string(s.scope.org), next)
	return err
}
