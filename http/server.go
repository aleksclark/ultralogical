// Package http is the transport adapter between the ultra domain and the
// HTTP/ConnectRPC protocol. All HTTP and Connect code is isolated here
// (dependency-grouped per agent_docs/package_layout.md); handlers are thin:
// authenticate, resolve org, check membership, call the store, convert.
package http

import (
	"log/slog"
	"net/http"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/provider"
	"github.com/aleksclark/ultracore/resourcework"
	"github.com/aleksclark/ultracore/gen/go/core/v1/corev1connect"
	"github.com/aleksclark/ultracore/jobqueue"
	"github.com/aleksclark/ultracore/secrets"
)

// Config carries handler dependencies, injected by the main package.
type Config struct {
	Store uc.Store
	// Providers is the provider seam's registry. Registration builds the
	// adapter through it and probes the real control plane, so a stored
	// registration is one that has answered rather than one that parsed.
	Providers *provider.Registry
	Auth      uc.Authenticator
	Bus       uc.EventBus
	Log       *slog.Logger
	// Keyring encrypts credential payloads (write path only; decryption
	// happens in workers).
	Keyring secrets.Keyring
	// Enqueue enqueues step jobs transactionally with run creation.
	Enqueue jobqueue.TxEnqueuer
	// DefaultModel fills StartRun requests that omit a model config.
	DefaultModel uc.ModelConfig
	// Resources orchestrates development-environment lifecycle and ExecPreview.
	Resources *resourcework.Service
}

// NewHandler builds the full cored http.Handler: all Connect services under
// their generated paths plus /healthz. Serve it with unencrypted HTTP/2
// enabled (http.Server.Protocols) so gRPC and Connect streaming work over
// cleartext.
func NewHandler(cfg Config) http.Handler {
	interceptors := connect.WithInterceptors(NewAuthInterceptor(cfg.Auth))

	mux := http.NewServeMux()

	tenantPath, tenantH := corev1connect.NewTenantServiceHandler(&tenantHandler{store: cfg.Store, keyring: cfg.Keyring, providers: cfg.Providers}, interceptors)
	mux.Handle(tenantPath, tenantH)

	sessPath, sessH := corev1connect.NewSessionServiceHandler(&sessionHandler{store: cfg.Store}, interceptors)
	mux.Handle(sessPath, sessH)

	agentPath, agentH := corev1connect.NewAgentServiceHandler(&agentHandler{
		store: cfg.Store, enqueue: cfg.Enqueue, defaultModel: cfg.DefaultModel,
	}, interceptors)
	mux.Handle(agentPath, agentH)
	automationPath, automationH := corev1connect.NewAutomationServiceHandler(&automationHandler{store: cfg.Store}, interceptors)
	mux.Handle(automationPath, automationH)

	if cfg.Resources != nil {
		resourcePath, resourceH := corev1connect.NewResourceServiceHandler(&resourceHandler{store: cfg.Store, resources: cfg.Resources}, interceptors)
		mux.Handle(resourcePath, resourceH)
	}

	// The unary interceptor covers Append; Subscribe is a streaming RPC and
	// authenticates inside the handler.
	evPath, evH := corev1connect.NewEventServiceHandler(&eventHandler{store: cfg.Store, auth: cfg.Auth, bus: cfg.Bus}, interceptors)
	mux.Handle(evPath, evH)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Consumers may call from browsers; CORS is open here and restricted at
	// the edge in production deployments.
	return cors(mux)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Expose-Headers", "Connect-Accept-Encoding, Connect-Content-Encoding, Connect-Error-Code, Connect-Error-Message")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
