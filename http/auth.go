package http

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
)

type ctxKey struct{}

// authContext is what the interceptor attaches after a successful key lookup.
type authContext struct {
	Identity uc.AuthIdentity
	Actor    uc.Actor
}

// identityFrom extracts the authenticated identity set by the auth interceptor
// or streaming-handler authentication.
func identityFrom(ctx context.Context) (authContext, bool) {
	a, ok := ctx.Value(ctxKey{}).(authContext)
	return a, ok
}

// actorFrom returns the caller's Actor, or a zero Actor.
func actorFrom(ctx context.Context) uc.Actor {
	a, ok := identityFrom(ctx)
	if !ok {
		return uc.Actor{}
	}
	return a.Actor
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

// authenticate resolves the request's bearer token via the domain
// Authenticator and attaches (TenantID, KeyScope, Actor) to context.
func authenticate(ctx context.Context, auth uc.Authenticator, authorization, actorHeader string) (context.Context, error) {
	token := bearer(authorization)
	if token == "" {
		return ctx, errUnauthenticated()
	}
	id, err := auth.Authenticate(ctx, token)
	if err != nil {
		return ctx, errUnauthenticated()
	}
	actor := uc.ParseActorHeader(actorHeader)
	if actor.Kind == "" && actor.ID == "" {
		// Default attribution: the key itself, as a service actor.
		actor = uc.Actor{Kind: uc.ActorKindService, ID: string(id.KeyID)}
	}
	return context.WithValue(ctx, ctxKey{}, authContext{Identity: id, Actor: actor}), nil
}

// NewAuthInterceptor authenticates every unary RPC and attaches identity + actor.
func NewAuthInterceptor(auth uc.Authenticator) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			ctx, err := authenticate(ctx, auth, req.Header().Get("Authorization"), req.Header().Get("X-Core-Actor"))
			if err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
	}
}

// requireTenant ensures the caller's key belongs to the named tenant.
// Missing and cross-tenant collapse to the same not-found.
func requireTenant(ctx context.Context, tenant uc.TenantID) (authContext, error) {
	a, ok := identityFrom(ctx)
	if !ok {
		return authContext{}, errUnauthenticated()
	}
	if a.Identity.TenantID != tenant {
		return authContext{}, errNotFound()
	}
	return a, nil
}

// requireAdmin ensures the caller's key is admin-scoped for the tenant.
func requireAdmin(ctx context.Context, tenant uc.TenantID) error {
	a, err := requireTenant(ctx, tenant)
	if err != nil {
		return err
	}
	if a.Identity.Scope != uc.KeyScopeAdmin {
		return connect.NewError(connect.CodePermissionDenied, errors.New("requires admin key"))
	}
	return nil
}
