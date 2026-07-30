package postgres

import (
	"context"
	"errors"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/jackc/pgx/v5"
)

type flowStore struct{ scope *orgScope }

func (s *flowStore) Put(ctx context.Context, f ultra.Flow) (ultra.Flow, error) {
	var version int
	err := s.scope.s.db().QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM flows WHERE org_id=$1 AND name=$2`, string(s.scope.org), f.Name).Scan(&version)
	if err != nil {
		return f, err
	}
	f.Version = version
	err = s.scope.s.db().QueryRow(ctx, `INSERT INTO flows(id,org_id,name,version,definition)VALUES($1,$2,$3,$4,$5)RETURNING created_at`, string(f.ID), string(s.scope.org), f.Name, f.Version, f.Definition).Scan(&f.CreatedAt)
	return f, err
}
func (s *flowStore) Get(ctx context.Context, name string, version int) (ultra.Flow, error) {
	var f ultra.Flow
	var row pgx.Row
	if version == 0 {
		row = s.scope.s.db().QueryRow(ctx, `SELECT id,org_id,name,version,definition,created_at FROM flows WHERE org_id=$1 AND name=$2 ORDER BY version DESC LIMIT 1`, string(s.scope.org), name)
	} else {
		row = s.scope.s.db().QueryRow(ctx, `SELECT id,org_id,name,version,definition,created_at FROM flows WHERE org_id=$1 AND name=$2 AND version=$3`, string(s.scope.org), name, version)
	}
	err := row.Scan(&f.ID, &f.OrgID, &f.Name, &f.Version, &f.Definition, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, ultra.ErrNotFound
	}
	return f, err
}
func (s *flowStore) List(ctx context.Context) ([]ultra.Flow, error) {
	rows, err := s.scope.s.db().Query(ctx, `SELECT DISTINCT ON(name) id,org_id,name,version,definition,created_at FROM flows WHERE org_id=$1 ORDER BY name,version DESC`, string(s.scope.org))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ultra.Flow
	for rows.Next() {
		var f ultra.Flow
		if err := rows.Scan(&f.ID, &f.OrgID, &f.Name, &f.Version, &f.Definition, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (s *flowStore) CreateInvocation(ctx context.Context, i ultra.FlowInvocation) error {
	_, err := s.scope.s.db().Exec(ctx, `INSERT INTO flow_invocations(id,org_id,session_id,flow_id,flow_name,flow_version,params)VALUES($1,$2,$3,$4,$5,$6,$7)`, string(i.ID), string(s.scope.org), string(i.SessionID), string(i.FlowID), i.FlowName, i.FlowVersion, i.Params)
	return err
}
