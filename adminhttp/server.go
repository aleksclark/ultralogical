// Package adminhttp is the transport adapter for the private operator admin
// API. It is never imported by cored or the consumer http package.
package adminhttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/aleksclark/ultracore/gen/go/admin/v1/adminv1connect"
	"github.com/aleksclark/ultracore/postgres"
)

// Config carries admin handler dependencies.
type Config struct {
	Store *postgres.AdminStore
	// Token is the required bearer operator token. Empty is only allowed when
	// DevMode is true (local development).
	Token string
	// DevMode disables the auth-required startup check and accepts any
	// non-empty bearer when Token is empty. Production must leave this false.
	DevMode bool
	// CORSOrigin, when set, enables CORS for that single origin (admin SPA).
	// Empty disables CORS entirely.
	CORSOrigin string
	Log        *slog.Logger
	// Ready reports process readiness (pg ping).
	Ready func() error
}

// NewHandler builds the coreadmin http.Handler: AdminReadService plus health.
func NewHandler(cfg Config) http.Handler {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	svc := &readService{store: cfg.Store}
	path, h := adminv1connect.NewAdminReadServiceHandler(svc, connect.WithInterceptors(newAuthInterceptor(cfg)))

	mux := http.NewServeMux()
	mux.Handle(path, h)
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
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newAuthInterceptor(cfg Config) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			token := bearer(req.Header().Get("Authorization"))
			if cfg.Token != "" {
				if subtleConstantTimeEq(token, cfg.Token) {
					return next(ctx, req)
				}
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing or invalid operator credentials"))
			}
			if cfg.DevMode && token != "" {
				return next(ctx, req)
			}
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing or invalid operator credentials"))
		}
	}
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
