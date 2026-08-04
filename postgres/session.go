package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	uc "github.com/aleksclark/ultracore"
)

// tenantScope filters every query by tenant id, making cross-tenant access
// structurally impossible at this layer.
type tenantScope struct {
	s   *Store
	tenant uc.TenantID
}

func (o *tenantScope) Sessions() uc.SessionStore               { return &sessionStore{o} }
func (o *tenantScope) Events() uc.EventStore                   { return &eventStore{o} }
func (o *tenantScope) Runs() uc.RunStore                       { return &runStore{o} }
func (o *tenantScope) Credentials() uc.CredentialStore         { return &credentialStore{o} }
func (o *tenantScope) Resources() uc.ResourceStore             { return &resourceStore{o} }
func (o *tenantScope) Providers() uc.ProviderInstanceStore     { return &providerStore{o} }
func (o *tenantScope) Memory() uc.SessionMemoryStore           { return &memoryStore{o} }
func (o *tenantScope) Waits() uc.RunWaitStore                  { return &waitStore{o} }
func (o *tenantScope) PeriodicPrompts() uc.PeriodicPromptStore { return &periodicStore{o} }

type sessionStore struct{ scope *tenantScope }

func (st *sessionStore) Create(ctx context.Context, s uc.Session) error {
	if err := uc.ValidateLabels(s.Labels); err != nil {
		return err
	}
	labels, err := json.Marshal(s.Labels)
	if err != nil {
		return fmt.Errorf("postgres: encode labels: %w", err)
	}
	if s.Labels == nil {
		labels = []byte("{}")
	}
	_, err = st.scope.s.db().Exec(ctx,
		`INSERT INTO sessions (id, tenant_id, title, labels) VALUES ($1, $2, $3, $4)`,
		string(s.ID), string(st.scope.tenant), s.Title, labels)
	if isUniqueViolation(err) {
		return uc.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create session: %w", err)
	}
	return nil
}

func (st *sessionStore) scan(row pgx.Row) (uc.Session, error) {
	var s uc.Session
	var labels []byte
	err := row.Scan(&s.ID, &s.TenantID, &s.Title, &labels, &s.CreatedAt, &s.ArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uc.Session{}, uc.ErrNotFound
	}
	if err != nil {
		return uc.Session{}, fmt.Errorf("postgres: scan session: %w", err)
	}
	if len(labels) > 0 && string(labels) != "null" {
		if err := json.Unmarshal(labels, &s.Labels); err != nil {
			return uc.Session{}, fmt.Errorf("postgres: decode labels: %w", err)
		}
	}
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	return s, nil
}

const sessionColumns = `id, tenant_id, title, labels, created_at, archived_at`

func (st *sessionStore) Get(ctx context.Context, id uc.SessionID) (uc.Session, error) {
	return st.scan(st.scope.s.db().QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE id = $1 AND tenant_id = $2`,
		string(id), string(st.scope.tenant)))
}

func (st *sessionStore) List(ctx context.Context, selectors []uc.LabelSelector) ([]uc.Session, error) {
	q := `SELECT ` + sessionColumns + ` FROM sessions WHERE tenant_id = $1`
	args := []any{string(st.scope.tenant)}
	for _, sel := range selectors {
		switch sel.Op {
		case "=", "eq", "":
			if len(sel.Values) != 1 {
				return nil, fmt.Errorf("postgres: equality selector needs one value")
			}
			args = append(args, map[string]string{sel.Key: sel.Values[0]})
			q += fmt.Sprintf(` AND labels @> $%d::jsonb`, len(args))
		case "in":
			if len(sel.Values) == 0 {
				// in () matches nothing
				return nil, nil
			}
			// OR of equality containments for each value.
			parts := make([]string, 0, len(sel.Values))
			for _, v := range sel.Values {
				args = append(args, map[string]string{sel.Key: v})
				parts = append(parts, fmt.Sprintf("labels @> $%d::jsonb", len(args)))
			}
			q += ` AND (` + strings.Join(parts, " OR ") + `)`
		default:
			return nil, fmt.Errorf("postgres: unknown selector op %q", sel.Op)
		}
	}
	q += ` ORDER BY created_at DESC`
	rows, err := st.scope.s.db().Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list sessions: %w", err)
	}
	defer rows.Close()
	var sessions []uc.Session
	for rows.Next() {
		s, err := st.scan(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func (st *sessionStore) UpdateLabels(ctx context.Context, id uc.SessionID, labels map[string]string) (uc.Session, error) {
	if err := uc.ValidateLabels(labels); err != nil {
		return uc.Session{}, err
	}
	if labels == nil {
		labels = map[string]string{}
	}
	b, err := json.Marshal(labels)
	if err != nil {
		return uc.Session{}, fmt.Errorf("postgres: encode labels: %w", err)
	}
	tag, err := st.scope.s.db().Exec(ctx,
		`UPDATE sessions SET labels = $3 WHERE id = $1 AND tenant_id = $2`,
		string(id), string(st.scope.tenant), b)
	if err != nil {
		return uc.Session{}, fmt.Errorf("postgres: update labels: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return uc.Session{}, uc.ErrNotFound
	}
	return st.Get(ctx, id)
}


func (st *sessionStore) Archive(ctx context.Context, id uc.SessionID) (uc.Session, error) {
	tag, err := st.scope.s.db().Exec(ctx,
		`UPDATE sessions SET archived_at = COALESCE(archived_at, now()) WHERE id = $1 AND tenant_id = $2`,
		string(id), string(st.scope.tenant))
	if err != nil {
		return uc.Session{}, fmt.Errorf("postgres: archive session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return uc.Session{}, uc.ErrNotFound
	}
	return st.Get(ctx, id)
}

type eventStore struct{ scope *tenantScope }

// Append assigns the next per-session seq by bumping sessions.last_seq inside
// a transaction, inserts the event, and pg_notify()s subscribers in the same
// transaction so the wakeup is atomic with visibility.
func (e *eventStore) Append(ctx context.Context, sessionID uc.SessionID, ev uc.Event) (int64, error) {
	var seq int64
	err := e.scope.s.Tx(ctx, func(txStore uc.Store) error {
		ps, ok := txStore.(*Store)
		if !ok {
			return errors.New("postgres: unexpected tx store type")
		}
		row := ps.db().QueryRow(ctx,
			`UPDATE sessions SET last_seq = last_seq + 1
			  WHERE id = $1 AND tenant_id = $2 RETURNING last_seq`,
			string(sessionID), string(e.scope.tenant))
		if err := row.Scan(&seq); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return uc.ErrNotFound
			}
			return fmt.Errorf("postgres: bump seq: %w", err)
		}
		payload := ev.Payload
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		if _, err := ps.db().Exec(ctx,
			`INSERT INTO session_events (session_id, seq, actor_type, actor_id, actor_display, kind, payload)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			string(sessionID), seq, ev.Actor.Kind, ev.Actor.ID, ev.Actor.Display, ev.Kind, payload); err != nil {
			return fmt.Errorf("postgres: insert event: %w", err)
		}
		if _, err := ps.db().Exec(ctx,
			`SELECT pg_notify($1, $2 || ':' || $3::bigint::text)`,
			EventChannel, string(sessionID), seq); err != nil {
			return fmt.Errorf("postgres: notify: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return seq, nil
}

func (e *eventStore) Range(ctx context.Context, sessionID uc.SessionID, fromSeq int64, limit int) ([]uc.Event, error) {
	if limit <= 0 {
		limit = 256
	}
	rows, err := e.scope.s.db().Query(ctx,
		`SELECT ev.session_id, ev.seq, ev.ts, ev.actor_type, ev.actor_id, COALESCE(ev.actor_display,''), ev.kind, ev.payload
		   FROM session_events ev
		   JOIN sessions s ON s.id = ev.session_id
		  WHERE ev.session_id = $1 AND s.tenant_id = $2 AND ev.seq > $3
		  ORDER BY ev.seq ASC LIMIT $4`,
		string(sessionID), string(e.scope.tenant), fromSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: range events: %w", err)
	}
	defer rows.Close()
	var events []uc.Event
	for rows.Next() {
		var ev uc.Event
		if err := rows.Scan(&ev.SessionID, &ev.Seq, &ev.TS, &ev.Actor.Kind, &ev.Actor.ID, &ev.Actor.Display, &ev.Kind, &ev.Payload); err != nil {
			return nil, fmt.Errorf("postgres: scan event: %w", err)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}
