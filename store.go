package core

import (
	"context"
	"errors"
)

// Sentinel errors. Store implementations translate backend errors into these
// so handlers can map them to typed API errors. Handlers deliberately return
// the same "not found" answer for missing rows and cross-tenant access so
// resource existence never leaks across tenants.
var (
	ErrNotFound         = errors.New("not found")
	ErrAlreadyExists    = errors.New("already exists")
	ErrPermissionDenied = errors.New("permission denied")
)

// Store is the root data-access seam. All tenant data access flows through
// Tenant(id), which returns a tenant-scoped handle: every query it issues
// carries the tenant id, making cross-tenant reads structurally impossible
// rather than merely checked.
type Store interface {
	// Tenants manages tenant lifecycle.
	Tenants() TenantStore
	// APIKeys manages tenant API keys (lookup is global by hash; creation is
	// tenant-scoped via the key's TenantID field).
	APIKeys() APIKeyStore
	// Tenant returns a tenant-scoped view. It does not verify the tenant
	// exists; scoped queries simply match nothing for a bogus id.
	Tenant(id TenantID) TenantScope
	// SessionTenant is the single directory lookup: which tenant owns a
	// session. Callers must collapse "no such session" and "not yours" into
	// the same error.
	SessionTenant(ctx context.Context, id SessionID) (TenantID, error)
	// Tx runs fn inside a transaction, providing a transaction-bound Store.
	// Nested calls reuse the outer transaction.
	Tx(ctx context.Context, fn func(Store) error) error
}

// TenantStore manages tenants.
type TenantStore interface {
	Create(ctx context.Context, t Tenant) error
	Get(ctx context.Context, id TenantID) (Tenant, error)
	List(ctx context.Context) ([]Tenant, error)
}

// APIKeyStore manages API keys. GetByHash is the auth path; Create/Revoke/
// List are the admin path.
type APIKeyStore interface {
	Create(ctx context.Context, k APIKey) error
	// GetByHash looks up a live or revoked key by SHA-256 digest. Callers
	// must still check RevokedAt.
	GetByHash(ctx context.Context, hash []byte) (APIKey, error)
	Get(ctx context.Context, id APIKeyID) (APIKey, error)
	List(ctx context.Context, tenant TenantID) ([]APIKeyInfo, error)
	// Revoke stamps revoked_at (idempotent). Returns ErrNotFound when the
	// key is missing or belongs to another tenant.
	Revoke(ctx context.Context, tenant TenantID, id APIKeyID) error
}

// TenantScope is the tenant-scoped data surface. Everything reachable from it
// is automatically filtered to the tenant it was created for.
type TenantScope interface {
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

// LabelSelector is one equality or set-membership predicate on session labels.
// Op is "=" or "in". Values has length 1 for equality.
type LabelSelector struct {
	Key    string
	Op     string   // "=" or "in"
	Values []string
}

// SessionStore manages sessions within one tenant.
type SessionStore interface {
	Create(ctx context.Context, s Session) error
	// Get returns ErrNotFound for sessions that exist in another tenant.
	Get(ctx context.Context, id SessionID) (Session, error)
	// List returns sessions matching every selector (AND). An empty selector
	// list returns all sessions in the tenant.
	List(ctx context.Context, selectors []LabelSelector) ([]Session, error)
	// UpdateLabels replaces the full label map and returns the new session.
	UpdateLabels(ctx context.Context, id SessionID, labels map[string]string) (Session, error)
	// Archive stamps archived_at (idempotent) and returns the session.
	Archive(ctx context.Context, id SessionID) (Session, error)
}

// EventStore is the append-only session event log within one tenant.
type EventStore interface {
	// Append assigns the next per-session sequence number, persists the
	// event, and (in the same transaction) notifies subscribers. Returns the
	// assigned seq.
	Append(ctx context.Context, sessionID SessionID, e Event) (int64, error)
	// Range reads events with seq > fromSeq, ascending, up to limit.
	Range(ctx context.Context, sessionID SessionID, fromSeq int64, limit int) ([]Event, error)
}
