package http

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
)

type ctxKey struct{}

// userFrom extracts the authenticated user set by the auth interceptor or
// streaming-handler authentication.
func userFrom(ctx context.Context) (uc.User, bool) {
	u, ok := ctx.Value(ctxKey{}).(uc.User)
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

// authenticate resolves the request's bearer token via the domain
// Authenticator. Both the unary interceptor and streaming handlers use it.
func authenticate(ctx context.Context, auth uc.Authenticator, authorization string) (context.Context, error) {
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
func NewAuthInterceptor(auth uc.Authenticator) connect.UnaryInterceptorFunc {
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
