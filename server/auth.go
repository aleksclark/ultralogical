package server

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"

	ultra "github.com/aleksclark/ultralogical"
)

// Authenticator resolves a bearer token to a user. Phase 0 ships only the
// static dev-token implementation; Phase 7 adds OIDC behind this same seam.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (ultra.User, error)
}

// DevTokenAuthenticator maps static tokens to user emails. Users must already
// exist in the store (the harness or an operator seeds them).
type DevTokenAuthenticator struct {
	store  ultra.Store
	tokens map[string]string // token -> email
}

// NewDevTokenAuthenticator builds an authenticator from a token->email map.
func NewDevTokenAuthenticator(store ultra.Store, tokens map[string]string) *DevTokenAuthenticator {
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
func (a *DevTokenAuthenticator) Authenticate(ctx context.Context, token string) (ultra.User, error) {
	email, ok := a.tokens[token]
	if !ok {
		return ultra.User{}, ultra.ErrPermissionDenied
	}
	return a.store.Users().GetByEmail(ctx, email)
}

type ctxKey struct{}

// userFrom extracts the authenticated user set by the auth interceptor.
func userFrom(ctx context.Context) (ultra.User, bool) {
	u, ok := ctx.Value(ctxKey{}).(ultra.User)
	return u, ok
}

// bearer extracts the bearer token from an Authorization header value.
func bearer(header string) string {
	const prefix = "Bearer "
	if strings.HasPrefix(header, prefix) {
		return header[len(prefix):]
	}
	return ""
}

func errUnauthenticated() *connect.Error {
	return connect.NewError(connect.CodeUnauthenticated, errors.New("missing or invalid credentials"))
}

// authenticate resolves the request's bearer token. Both unary interceptor
// and streaming handlers use it.
func authenticate(ctx context.Context, auth Authenticator, authorization string) (context.Context, error) {
	token := bearer(authorization)
	if token == "" {
		return ctx, errUnauthenticated()
	}
	user, err := auth.Authenticate(ctx, token)
	if err != nil {
		return ctx, errUnauthenticated()
	}
	return context.WithValue(ctx, ctxKey{}, user), nil
}

// NewAuthInterceptor authenticates every unary RPC.
func NewAuthInterceptor(auth Authenticator) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			ctx, err := authenticate(ctx, auth, req.Header().Get("Authorization"))
			if err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
	}
}
