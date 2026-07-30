package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	ultra "github.com/aleksclark/ultralogical"
)

type orgStore struct{ s *Store }

func (o *orgStore) Create(ctx context.Context, org ultra.Org) error {
	_, err := o.s.db().Exec(ctx,
		`INSERT INTO orgs (id, name, plan) VALUES ($1, $2, COALESCE(NULLIF($3, ''), 'dev'))`,
		string(org.ID), org.Name, org.Plan)
	if isUniqueViolation(err) {
		return ultra.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create org: %w", err)
	}
	return nil
}

func (o *orgStore) Get(ctx context.Context, id ultra.OrgID) (ultra.Org, error) {
	var org ultra.Org
	err := o.s.db().QueryRow(ctx,
		`SELECT id, name, plan, created_at FROM orgs WHERE id = $1`, string(id)).
		Scan(&org.ID, &org.Name, &org.Plan, &org.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ultra.Org{}, ultra.ErrNotFound
	}
	if err != nil {
		return ultra.Org{}, fmt.Errorf("postgres: get org: %w", err)
	}
	return org, nil
}

func (o *orgStore) AddMember(ctx context.Context, m ultra.OrgMember) error {
	_, err := o.s.db().Exec(ctx,
		`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)`,
		string(m.OrgID), string(m.UserID), string(m.Role))
	if isUniqueViolation(err) {
		return ultra.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: add member: %w", err)
	}
	return nil
}

func (o *orgStore) ListMembers(ctx context.Context, id ultra.OrgID) ([]ultra.OrgMember, error) {
	rows, err := o.s.db().Query(ctx,
		`SELECT org_id, user_id, role, joined_at FROM org_members WHERE org_id = $1 ORDER BY joined_at`,
		string(id))
	if err != nil {
		return nil, fmt.Errorf("postgres: list members: %w", err)
	}
	defer rows.Close()
	var members []ultra.OrgMember
	for rows.Next() {
		var m ultra.OrgMember
		if err := rows.Scan(&m.OrgID, &m.UserID, &m.Role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan member: %w", err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (o *orgStore) MemberRole(ctx context.Context, id ultra.OrgID, user ultra.UserID) (ultra.OrgRole, error) {
	var role ultra.OrgRole
	err := o.s.db().QueryRow(ctx,
		`SELECT role FROM org_members WHERE org_id = $1 AND user_id = $2`,
		string(id), string(user)).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ultra.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("postgres: member role: %w", err)
	}
	return role, nil
}

func (o *orgStore) ListForUser(ctx context.Context, user ultra.UserID) ([]ultra.Org, error) {
	rows, err := o.s.db().Query(ctx,
		`SELECT o.id, o.name, o.plan, o.created_at
		   FROM orgs o JOIN org_members m ON m.org_id = o.id
		  WHERE m.user_id = $1 ORDER BY o.created_at`, string(user))
	if err != nil {
		return nil, fmt.Errorf("postgres: list orgs for user: %w", err)
	}
	defer rows.Close()
	var orgs []ultra.Org
	for rows.Next() {
		var o ultra.Org
		if err := rows.Scan(&o.ID, &o.Name, &o.Plan, &o.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan org: %w", err)
		}
		orgs = append(orgs, o)
	}
	return orgs, rows.Err()
}

type userStore struct{ s *Store }

func (u *userStore) Create(ctx context.Context, user ultra.User) error {
	_, err := u.s.db().Exec(ctx,
		`INSERT INTO users (id, email, display) VALUES ($1, $2, $3)`,
		string(user.ID), user.Email, user.Display)
	if isUniqueViolation(err) {
		return ultra.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create user: %w", err)
	}
	return nil
}

func (u *userStore) Get(ctx context.Context, id ultra.UserID) (ultra.User, error) {
	return u.scanOne(ctx, `SELECT id, email, display, created_at FROM users WHERE id = $1`, string(id))
}

func (u *userStore) GetByEmail(ctx context.Context, email string) (ultra.User, error) {
	return u.scanOne(ctx, `SELECT id, email, display, created_at FROM users WHERE email = $1`, email)
}

func (u *userStore) scanOne(ctx context.Context, sql string, arg any) (ultra.User, error) {
	var user ultra.User
	err := u.s.db().QueryRow(ctx, sql, arg).
		Scan(&user.ID, &user.Email, &user.Display, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ultra.User{}, ultra.ErrNotFound
	}
	if err != nil {
		return ultra.User{}, fmt.Errorf("postgres: get user: %w", err)
	}
	return user, nil
}
