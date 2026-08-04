package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

// KeyScope is the authority granted by one API key.
type KeyScope string

const (
	// KeyScopeAdmin may manage the tenant: keys, credentials, providers.
	KeyScopeAdmin KeyScope = "admin"
	// KeyScopeSessions may use the session/run/resource surface only.
	KeyScopeSessions KeyScope = "sessions"
)

// APIKeyID identifies one tenant API key.
type APIKeyID string

// APIKey is a tenant credential. Cleartext is never stored: only a SHA-256
// hash (for lookup) and an AES-GCM ciphertext (for redactor re-registration
// after restart) sit at rest. Prefix is the non-secret display fragment.
type APIKey struct {
	ID        APIKeyID
	TenantID  TenantID
	Name      string
	Scope     KeyScope
	Prefix    string
	KeyHash   []byte
	KeyEnc    []byte
	CreatedAt time.Time
	RevokedAt *time.Time
}

// APIKeyInfo is the listable, secret-free view of a key.
type APIKeyInfo struct {
	ID        APIKeyID
	TenantID  TenantID
	Name      string
	Scope     KeyScope
	Prefix    string
	CreatedAt time.Time
	RevokedAt *time.Time
}

// Info returns the secret-free view.
func (k APIKey) Info() APIKeyInfo {
	return APIKeyInfo{
		ID: k.ID, TenantID: k.TenantID, Name: k.Name, Scope: k.Scope,
		Prefix: k.Prefix, CreatedAt: k.CreatedAt, RevokedAt: k.RevokedAt,
	}
}

// AuthIdentity is what a successful key lookup proves: which tenant, which
// key, and what that key may do. The opaque Actor is supplied per call by
// the consumer and is not part of the key.
type AuthIdentity struct {
	TenantID TenantID
	KeyID    APIKeyID
	Scope    KeyScope
}

// Authenticator resolves a bearer token to a tenant identity. Transport
// adapters extract the credential; implementations decide what it means.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (AuthIdentity, error)
}

// APIKeyAuthenticator looks up live (non-revoked) API keys by SHA-256 hash.
// It depends only on domain types, so it lives in the root package.
type APIKeyAuthenticator struct {
	store Store
}

// NewAPIKeyAuthenticator builds an authenticator over the given store.
func NewAPIKeyAuthenticator(store Store) *APIKeyAuthenticator {
	return &APIKeyAuthenticator{store: store}
}

// Authenticate implements Authenticator. Revoked and unknown keys both return
// ErrPermissionDenied so callers cannot distinguish them.
func (a *APIKeyAuthenticator) Authenticate(ctx context.Context, token string) (AuthIdentity, error) {
	if token == "" {
		return AuthIdentity{}, ErrPermissionDenied
	}
	key, err := a.store.APIKeys().GetByHash(ctx, HashAPIKey(token))
	if err != nil {
		return AuthIdentity{}, ErrPermissionDenied
	}
	if key.RevokedAt != nil {
		return AuthIdentity{}, ErrPermissionDenied
	}
	return AuthIdentity{TenantID: key.TenantID, KeyID: key.ID, Scope: key.Scope}, nil
}

// HashAPIKey returns the SHA-256 digest used as the at-rest lookup key.
func HashAPIKey(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// GenerateAPIKey mints a new raw key string. Format: "uck_" + 32 random bytes
// hex-encoded. The caller must hash/encrypt before persistence and register
// the raw value with the process redactor.
func GenerateAPIKey() (raw string, prefix string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("core: generate api key: %w", err)
	}
	raw = "uck_" + hex.EncodeToString(b[:])
	prefix = raw[:12] // "uck_" + 8 hex chars
	return raw, prefix, nil
}

// EncodeActorHeader serializes an Actor for the X-Core-Actor request header.
func EncodeActorHeader(a Actor) string {
	// Compact form kind/id; display is base64url-encoded when non-empty so
	// the header stays single-line and reversible.
	if a.Display == "" {
		return a.Kind + "/" + a.ID
	}
	return a.Kind + "/" + a.ID + "/" + base64.RawURLEncoding.EncodeToString([]byte(a.Display))
}

// ParseActorHeader parses the X-Core-Actor header. Empty input yields a zero
// Actor (callers may substitute a default).
func ParseActorHeader(s string) Actor {
	if s == "" {
		return Actor{}
	}
	kind, rest, ok := split2(s, '/')
	if !ok {
		return Actor{Kind: s}
	}
	id, enc, ok := split2(rest, '/')
	if !ok {
		return Actor{Kind: kind, ID: rest}
	}
	display, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return Actor{Kind: kind, ID: id, Display: enc}
	}
	return Actor{Kind: kind, ID: id, Display: string(display)}
}

func split2(s string, sep byte) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
