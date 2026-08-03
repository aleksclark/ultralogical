package core

import (
	"context"
	"errors"
)

// Sentinel errors. Store implementations translate backend errors into these
// so handlers can map them to typed API errors. Handlers deliberately return
// the same "not found" answer for missing rows and cross-tenant access so
// resource existence never leaks across orgs.
var (
	ErrNotFound         = errors.New("not found")
	ErrAlreadyExists    = errors.New("already exists")
	ErrPermissionDenied = errors.New("permission denied")
)

// Store is the root data-access seam. All tenant data access flows through
// Org(id), which returns an org-scoped handle: every query it issues carries
// the org id, making cross-tenant reads structurally impossible rather than
// merely checked.
type Store interface {
	// Orgs manages org lifecycle and membership (pre-tenant surface).
	Orgs() OrgStore
	// Users manages global user identities.
	Users() UserStore
	// Org returns an org-scoped view. It does not verify the org exists;
	// scoped queries simply match nothing for a bogus id.
	Org(id OrgID) OrgScope
	// SessionOrg is the single directory lookup: which org owns a session.
	// Callers must verify membership before acting on the answer and must
	// collapse "no such session" and "not a member" into the same error.
	SessionOrg(ctx context.Context, id SessionID) (OrgID, error)
	// Tx runs fn inside a transaction, providing a transaction-bound Store.
	// Nested calls reuse the outer transaction.
	Tx(ctx context.Context, fn func(Store) error) error
}

// OrgStore manages orgs and memberships.
type OrgStore interface {
	Create(ctx context.Context, o Org) error
	Get(ctx context.Context, id OrgID) (Org, error)
	AddMember(ctx context.Context, m OrgMember) error
	ListMembers(ctx context.Context, id OrgID) ([]OrgMember, error)
	// MemberRole returns ErrNotFound when the user is not a member.
	MemberRole(ctx context.Context, id OrgID, user UserID) (OrgRole, error)
	ListForUser(ctx context.Context, user UserID) ([]Org, error)
}

// UserStore manages global user identities.
type UserStore interface {
	Create(ctx context.Context, u User) error
	Get(ctx context.Context, id UserID) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
}

// OrgScope is the tenant-scoped data surface. Everything reachable from it is
// automatically filtered to the org it was created for.
type OrgScope interface {
	Sessions() SessionStore
	Events() EventStore
	Runs() RunStore
	Credentials() CredentialStore
	Resources() ResourceStore
	Providers() ProviderInstanceStore
	Memory() SessionMemoryStore
	Waits() RunWaitStore
	PeriodicPrompts() PeriodicPromptStore
}

// SessionStore manages sessions within one org.
type SessionStore interface {
	Create(ctx context.Context, s Session) error
	// Get returns ErrNotFound for sessions that exist in another org.
	Get(ctx context.Context, id SessionID) (Session, error)
	List(ctx context.Context) ([]Session, error)
}

// EventStore is the append-only session event log within one org.
type EventStore interface {
	// Append assigns the next per-session sequence number, persists the
	// event, and (in the same transaction) notifies subscribers. Returns the
	// assigned seq.
	Append(ctx context.Context, sessionID SessionID, e Event) (int64, error)
	// Range reads events with seq > fromSeq, ascending, up to limit.
	Range(ctx context.Context, sessionID SessionID, fromSeq int64, limit int) ([]Event, error)
}
