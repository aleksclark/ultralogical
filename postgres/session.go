package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	uc "github.com/aleksclark/ultracore"
)

// orgScope filters every query by org id, making cross-tenant access
// structurally impossible at this layer.
type orgScope struct {
	s   *Store
	org uc.OrgID
}

func (o *orgScope) Sessions() uc.SessionStore               { return &sessionStore{o} }
func (o *orgScope) Events() uc.EventStore                   { return &eventStore{o} }
func (o *orgScope) Runs() uc.RunStore                       { return &runStore{o} }
func (o *orgScope) Credentials() uc.CredentialStore         { return &credentialStore{o} }
func (o *orgScope) Envs() uc.EnvStore                       { return &envStore{o} }
func (o *orgScope) Providers() uc.ProviderInstanceStore     { return &providerStore{o} }
func (o *orgScope) Memory() uc.SessionMemoryStore           { return &memoryStore{o} }
func (o *orgScope) Waits() uc.RunWaitStore                  { return &waitStore{o} }
func (o *orgScope) PeriodicPrompts() uc.PeriodicPromptStore { return &periodicStore{o} }

type sessionStore struct{ scope *orgScope }

func (st *sessionStore) Create(ctx context.Context, s uc.Session) error {
	_, err := st.scope.s.db().Exec(ctx,
		`INSERT INTO sessions (id, org_id, title) VALUES ($1, $2, $3)`,
		string(s.ID), string(st.scope.org), s.Title)
	if isUniqueViolation(err) {
		return uc.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("postgres: create session: %w", err)
	}
	return nil
}

func (st *sessionStore) Get(ctx context.Context, id uc.SessionID) (uc.Session, error) {
	var s uc.Session
	err := st.scope.s.db().QueryRow(ctx,
		`SELECT id, org_id, title, created_at, archived_at
		   FROM sessions WHERE id = $1 AND org_id = $2`,
		string(id), string(st.scope.org)).
		Scan(&s.ID, &s.OrgID, &s.Title, &s.CreatedAt, &s.ArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uc.Session{}, uc.ErrNotFound
	}
	if err != nil {
		return uc.Session{}, fmt.Errorf("postgres: get session: %w", err)
	}
	return s, nil
}

func (st *sessionStore) List(ctx context.Context) ([]uc.Session, error) {
	rows, err := st.scope.s.db().Query(ctx,
		`SELECT id, org_id, title, created_at, archived_at
		   FROM sessions WHERE org_id = $1 ORDER BY created_at DESC`,
		string(st.scope.org))
	if err != nil {
		return nil, fmt.Errorf("postgres: list sessions: %w", err)
	}
	defer rows.Close()
	var sessions []uc.Session
	for rows.Next() {
		var s uc.Session
		if err := rows.Scan(&s.ID, &s.OrgID, &s.Title, &s.CreatedAt, &s.ArchivedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

type eventStore struct{ scope *orgScope }

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
			  WHERE id = $1 AND org_id = $2 RETURNING last_seq`,
			string(sessionID), string(e.scope.org))
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
			`INSERT INTO session_events (session_id, seq, actor_type, actor_id, kind, payload)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			string(sessionID), seq, string(ev.Actor.Type), ev.Actor.ID, ev.Kind, payload); err != nil {
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
		`SELECT ev.session_id, ev.seq, ev.ts, ev.actor_type, ev.actor_id, ev.kind, ev.payload
		   FROM session_events ev
		   JOIN sessions s ON s.id = ev.session_id
		  WHERE ev.session_id = $1 AND s.org_id = $2 AND ev.seq > $3
		  ORDER BY ev.seq ASC LIMIT $4`,
		string(sessionID), string(e.scope.org), fromSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: range events: %w", err)
	}
	defer rows.Close()
	var events []uc.Event
	for rows.Next() {
		var ev uc.Event
		if err := rows.Scan(&ev.SessionID, &ev.Seq, &ev.TS, &ev.Actor.Type, &ev.Actor.ID, &ev.Kind, &ev.Payload); err != nil {
			return nil, fmt.Errorf("postgres: scan event: %w", err)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}
