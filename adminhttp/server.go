// Package adminhttp is the transport adapter for the private operator admin
// API. It is never imported by cored or the consumer http package.
package adminhttp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/aleksclark/ultracore/admin/authz"
	"github.com/aleksclark/ultracore/admin/command"
	adminstore "github.com/aleksclark/ultracore/admin/store"
	"github.com/aleksclark/ultracore/gen/go/admin/v1/adminv1connect"
)

// Config carries admin handler dependencies.
type Config struct {
	Store *adminstore.AdminStore
	// Tokens resolves operator bearer tokens. When nil, Token is used as a
	// single admin-role token (test convenience).
	Tokens *authz.TokenDirectory
	// Token is a single operator bearer (mapped to admin unless Tokens set).
	Token string
	// DevMode accepts any non-empty bearer as admin when no tokens configured.
	DevMode bool
	// CORSOrigin, when set, enables CORS for that single origin (admin SPA).
	CORSOrigin string
	Log        *slog.Logger
	// Ready reports process readiness (pg ping).
	Ready func() error
	// Engine runs AdminCommandService RPCs. Optional — when nil, command routes
	// are not mounted.
	Engine *command.Engine
	// RevealEnabled surfaces WhoAmI.reveal_enabled (Engine also enforces).
	RevealEnabled bool
}

func (cfg Config) directory() *authz.TokenDirectory {
	if cfg.Tokens != nil {
		cfg.Tokens.DevMode = cfg.DevMode || cfg.Tokens.DevMode
		return cfg.Tokens
	}
	if cfg.Token != "" {
		return authz.DirectoryFromEntries([]authz.TokenEntry{{
			Token: cfg.Token, Role: authz.RoleAdmin, Name: "default", ID: "default",
		}}, cfg.DevMode)
	}
	return &authz.TokenDirectory{DevMode: cfg.DevMode}
}

// NewHandler builds the coreadmin http.Handler: AdminReadService, optional
// AdminCommandService, plus health.
func NewHandler(cfg Config) http.Handler {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	dir := cfg.directory()
	auth := newAuthInterceptor(dir)

	read := &readService{store: cfg.Store, tokens: dir, revealEnabled: cfg.RevealEnabled}
	readPath, readH := adminv1connect.NewAdminReadServiceHandler(read, connect.WithInterceptors(auth))

	mux := http.NewServeMux()
	mux.Handle(readPath, readH)

	if cfg.Engine != nil {
		cmd := &commandService{engine: cfg.Engine, revealEnabled: cfg.RevealEnabled}
		cmdPath, cmdH := adminv1connect.NewAdminCommandServiceHandler(cmd, connect.WithInterceptors(auth))
		mux.Handle(cmdPath, cmdH)
	}

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if cfg.Ready != nil {
			if err := cfg.Ready(); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	var handler http.Handler = mux
	if cfg.CORSOrigin != "" {
		handler = cors(cfg.CORSOrigin, handler)
	}
	return handler
}

func cors(origin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms, X-Admin-Reauth, X-Request-Id")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type ctxKey int

const (
	ctxOperator ctxKey = iota + 1
	ctxReauthOK
	ctxSourceIP
	ctxRequestID
)

func newAuthInterceptor(dir *authz.TokenDirectory) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			token := bearer(req.Header().Get("Authorization"))
			op, ok := dir.Lookup(token)
			if !ok {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing or invalid operator credentials"))
			}
			ctx = context.WithValue(ctx, ctxOperator, op)
			reauth := req.Header().Get("X-Admin-Reauth")
			reauthOK := reauth != "" && subtleConstantTimeEq(reauth, token)
			ctx = context.WithValue(ctx, ctxReauthOK, reauthOK)
			if rid := req.Header().Get("X-Request-Id"); rid != "" {
				ctx = context.WithValue(ctx, ctxRequestID, rid)
			}
			if peer := req.Peer(); peer.Addr != "" {
				host, _, err := net.SplitHostPort(peer.Addr)
				if err != nil {
					host = peer.Addr
				}
				ctx = context.WithValue(ctx, ctxSourceIP, host)
			}
			return next(ctx, req)
		}
	}
}

func operatorFrom(ctx context.Context) (authz.Operator, bool) {
	op, ok := ctx.Value(ctxOperator).(authz.Operator)
	return op, ok
}

func reauthOK(ctx context.Context) bool {
	v, _ := ctx.Value(ctxReauthOK).(bool)
	return v
}

func sourceIP(ctx context.Context) string {
	v, _ := ctx.Value(ctxSourceIP).(string)
	return v
}

func requestID(ctx context.Context) string {
	v, _ := ctx.Value(ctxRequestID).(string)
	return v
}

func bearer(header string) string {
	const prefix = "Bearer "
	if strings.HasPrefix(header, prefix) {
		return header[len(prefix):]
	}
	return ""
}

func subtleConstantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
