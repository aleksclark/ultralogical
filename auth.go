package core

import (
	"context"
	"strings"
)

// Authenticator resolves a credential (bearer token) to a user. Transport
// adapters (the http package) extract the credential; implementations decide
// what it means. Phase 0 ships the static dev-token implementation below;
// Phase 7 adds OIDC behind this same seam.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (User, error)
}

// DevTokenAuthenticator maps static tokens to user emails. Users must
// already exist in the store (the harness or an operator seeds them). It
// depends only on domain types, so it lives in the root package.
type DevTokenAuthenticator struct {
	store  Store
	tokens map[string]string // token -> email
}

// NewDevTokenAuthenticator builds an authenticator from a token->email map.
func NewDevTokenAuthenticator(store Store, tokens map[string]string) *DevTokenAuthenticator {
	return &DevTokenAuthenticator{store: store, tokens: tokens}
}

// ParseDevTokens parses "token1=email1,token2=email2".
func ParseDevTokens(s string) map[string]string {
	out := map[string]string{}
	for pair := range strings.SplitSeq(s, ",") {
		if token, email, ok := strings.Cut(strings.TrimSpace(pair), "="); ok && token != "" && email != "" {
			out[token] = email
		}
	}
	return out
}

// Authenticate implements Authenticator.
func (a *DevTokenAuthenticator) Authenticate(ctx context.Context, token string) (User, error) {
	email, ok := a.tokens[token]
	if !ok {
		return User{}, ErrPermissionDenied
	}
	return a.store.Users().GetByEmail(ctx, email)
}
