// Package server wires the Connect RPC handlers, authentication, and event
// fan-out into an http.Handler. It is transport-only: all domain behavior
// lives in the root package and its store implementations.
package server

import (
	"log/slog"
	"net/http"

	"connectrpc.com/connect"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/gen/go/ultra/v1/ultrav1connect"
	"github.com/aleksclark/ultralogical/server/eventbus"
)

// Config carries server dependencies.
type Config struct {
	Store ultra.Store
	Auth  Authenticator
	Bus   *eventbus.Bus
	Log   *slog.Logger
}

// NewHandler builds the full ultrad http.Handler: all Connect services under
// their generated paths plus /healthz. Serve it with unencrypted HTTP/2
// enabled (http.Server.Protocols) so gRPC and Connect streaming work over
// cleartext.
func NewHandler(cfg Config) http.Handler {
	interceptors := connect.WithInterceptors(NewAuthInterceptor(cfg.Auth))

	mux := http.NewServeMux()

	orgPath, orgH := ultrav1connect.NewOrgServiceHandler(&orgHandler{store: cfg.Store}, interceptors)
	mux.Handle(orgPath, orgH)

	sessPath, sessH := ultrav1connect.NewSessionServiceHandler(&sessionHandler{store: cfg.Store}, interceptors)
	mux.Handle(sessPath, sessH)

	// The unary interceptor covers Append; Subscribe is a streaming RPC and
	// authenticates inside the handler.
	evPath, evH := ultrav1connect.NewEventServiceHandler(&eventHandler{store: cfg.Store, auth: cfg.Auth, bus: cfg.Bus}, interceptors)
	mux.Handle(evPath, evH)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return mux
}
