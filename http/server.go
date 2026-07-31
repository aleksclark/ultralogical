// Package http is the transport adapter between the ultra domain and the
// HTTP/ConnectRPC protocol. All HTTP and Connect code is isolated here
// (dependency-grouped per agent_docs/package_layout.md); handlers are thin:
// authenticate, resolve org, check membership, call the store, convert.
package http

import (
	"context"
	"log/slog"
	"net/http"

	"connectrpc.com/connect"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envwork"
	"github.com/aleksclark/ultralogical/gen/go/ultra/v1/ultrav1connect"
	"github.com/aleksclark/ultralogical/jobqueue"
	"github.com/aleksclark/ultralogical/secrets"
)

// Config carries handler dependencies, injected by the main package.
type Config struct {
	Store         ultra.Store
	ProviderKinds map[string]func(context.Context, []byte) error
	Auth          ultra.Authenticator
	Bus           ultra.EventBus
	Log           *slog.Logger
	// Keyring encrypts credential payloads (write path only; decryption
	// happens in workers).
	Keyring secrets.Keyring
	// Enqueue enqueues step jobs transactionally with run creation.
	Enqueue jobqueue.TxEnqueuer
	// DefaultModel fills StartRun requests that omit a model config.
	DefaultModel ultra.ModelConfig
	// Envs orchestrates development-environment lifecycle and ExecPreview.
	Envs *envwork.Service
}

// NewHandler builds the full ultrad http.Handler: all Connect services under
// their generated paths plus /healthz. Serve it with unencrypted HTTP/2
// enabled (http.Server.Protocols) so gRPC and Connect streaming work over
// cleartext.
func NewHandler(cfg Config) http.Handler {
	interceptors := connect.WithInterceptors(NewAuthInterceptor(cfg.Auth))

	mux := http.NewServeMux()

	orgPath, orgH := ultrav1connect.NewOrgServiceHandler(&orgHandler{store: cfg.Store, keyring: cfg.Keyring, providerKinds: cfg.ProviderKinds}, interceptors)
	mux.Handle(orgPath, orgH)

	sessPath, sessH := ultrav1connect.NewSessionServiceHandler(&sessionHandler{store: cfg.Store}, interceptors)
	mux.Handle(sessPath, sessH)

	agentPath, agentH := ultrav1connect.NewAgentServiceHandler(&agentHandler{
		store: cfg.Store, enqueue: cfg.Enqueue, defaultModel: cfg.DefaultModel,
	}, interceptors)
	mux.Handle(agentPath, agentH)
	automationPath, automationH := ultrav1connect.NewAutomationServiceHandler(&automationHandler{store: cfg.Store}, interceptors)
	mux.Handle(automationPath, automationH)
	flowPath, flowH := ultrav1connect.NewFlowServiceHandler(&flowHandler{store: cfg.Store, enqueue: cfg.Enqueue, defaultModel: cfg.DefaultModel}, interceptors)
	mux.Handle(flowPath, flowH)

	if cfg.Envs != nil {
		envPath, envH := ultrav1connect.NewEnvServiceHandler(&envHandler{store: cfg.Store, envs: cfg.Envs}, interceptors)
		mux.Handle(envPath, envH)
		billingPath, billingH := ultrav1connect.NewBillingServiceHandler(&billingHandler{store: cfg.Store}, interceptors)
		mux.Handle(billingPath, billingH)
	}

	// The unary interceptor covers Append; Subscribe is a streaming RPC and
	// authenticates inside the handler.
	evPath, evH := ultrav1connect.NewEventServiceHandler(&eventHandler{store: cfg.Store, auth: cfg.Auth, bus: cfg.Bus}, interceptors)
	mux.Handle(evPath, evH)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Browser clients are first-class. Phase 0/1 allows any origin; hosted
	// deployments restrict this via the edge proxy in Phase 7.
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
