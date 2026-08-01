package tunnel

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	ultra "github.com/aleksclark/ultralogical"
)

// Agent is the user-side control API. It owns a real provider (local Docker in
// the shipped agent) and exposes it over authenticated HTTP, which the user
// publishes through an outbound tunnel.
//
// Every request must carry both the org-scoped bearer token and a platform
// signature: possession of the tunnel URL, or of the token alone, is not
// enough to drive someone's machine.
type Agent struct {
	// Provider is the local provider the agent drives.
	Provider ultra.EnvProvider
	// Token is the org-scoped registration token the platform presents.
	Token string
	// Secret is the shared signing secret for control requests.
	Secret string
	// Endpoint rewrites a locally-published endpoint into one the platform can
	// reach through the tunnel. Left nil the local endpoint is returned
	// unchanged, which is correct when the tunnel forwards the port directly.
	Endpoint func(local string) string
	Log      *slog.Logger

	mu      sync.RWMutex
	revoked bool
	since   time.Time
}

// Handler builds the agent's HTTP surface.
func (a *Agent) Handler() http.Handler {
	if a.since.IsZero() {
		a.since = time.Now().UTC()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+PathHealth, a.health)
	mux.HandleFunc("POST "+PathProvision, a.guard(a.provision))
	mux.HandleFunc("POST "+PathStatus, a.guard(a.status))
	mux.HandleFunc("POST "+PathEndpoint, a.guard(a.endpoint))
	mux.HandleFunc("POST "+PathRestart, a.guard(a.restart))
	mux.HandleFunc("POST "+PathTerminate, a.guard(a.terminate))
	mux.HandleFunc("POST "+PathResources, a.guard(a.resources))
	mux.HandleFunc("POST "+PathRevoke, a.guard(a.revoke))
	return mux
}

// Revoke withdraws the agent's lease. A revoked agent keeps answering health
// so the platform can tell a revoked machine from an unreachable one, but
// refuses every operation and stops serving environments.
func (a *Agent) Revoke() {
	a.mu.Lock()
	a.revoked = true
	a.mu.Unlock()
}

// Revoked reports whether the lease has been withdrawn.
func (a *Agent) Revoked() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.revoked
}

func (a *Agent) health(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, HealthResponse{
		Status: "ok", Provider: ultra.ProviderKindTunnelLocal,
		ConnectedAt: a.since, Revoked: a.Revoked(),
	})
}

func (a *Agent) authorized(r *http.Request) bool {
	presented := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return presented != "" && presented == a.Token
}

// guard authenticates, verifies the platform signature, and refuses a revoked
// agent, before any handler sees the request.
func (a *Agent) guard(next func(http.ResponseWriter, *http.Request, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := readBody(r)
		if err != nil {
			http.Error(w, "unreadable body", http.StatusBadRequest)
			return
		}
		if err := VerifySignature(a.Secret, r.URL.Path,
			r.Header.Get(HeaderTimestamp), r.Header.Get(HeaderSignature), body, time.Now()); err != nil {
			if a.Log != nil {
				a.Log.Warn("tunnel: rejected an unsigned or stale control request",
					"path", r.URL.Path, "error", err.Error())
			}
			http.Error(w, "signature required", http.StatusForbidden)
			return
		}
		if a.Revoked() && r.URL.Path != PathRevoke {
			http.Error(w, "lease revoked", http.StatusGone)
			return
		}
		next(w, r, body)
	}
}

func (a *Agent) provision(w http.ResponseWriter, r *http.Request, body []byte) {
	var req ProvisionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	handle, err := a.Provider.Provision(r.Context(), req.EnvID, req.Spec, req.Token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, ProvisionResponse{Handle: handle})
}

func (a *Agent) status(w http.ResponseWriter, r *http.Request, body []byte) {
	var req HandleRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	status, err := a.Provider.Status(r.Context(), req.Handle)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, StatusResponse{State: status.State, Message: status.Message})
}

func (a *Agent) endpoint(w http.ResponseWriter, r *http.Request, body []byte) {
	var req HandleRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	local, err := a.Provider.Endpoint(r.Context(), req.Handle)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if a.Endpoint != nil {
		local = a.Endpoint(local)
	}
	writeJSON(w, EndpointResponse{Endpoint: local})
}

func (a *Agent) restart(w http.ResponseWriter, r *http.Request, body []byte) {
	var req RestartRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	handle, err := a.Provider.Restart(r.Context(), req.EnvID, req.Handle, req.Spec, req.Token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, ProvisionResponse{Handle: handle})
}

func (a *Agent) terminate(w http.ResponseWriter, r *http.Request, body []byte) {
	var req HandleRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := a.Provider.Terminate(r.Context(), req.Handle); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, struct{}{})
}

func (a *Agent) resources(w http.ResponseWriter, r *http.Request, body []byte) {
	var req HandleRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	lister, ok := a.Provider.(ultra.EnvResourceLister)
	if !ok {
		writeJSON(w, ResourcesResponse{})
		return
	}
	resources, err := lister.Resources(r.Context(), req.EnvID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, ResourcesResponse{Resources: resources})
}

// revoke withdraws the lease and releases everything the agent holds. A user
// revoking access expects their machine to stop running the platform's work,
// not merely to stop accepting new work.
func (a *Agent) revoke(w http.ResponseWriter, r *http.Request, body []byte) {
	var req HandleRequest
	_ = json.Unmarshal(body, &req)
	a.Revoke()
	if req.Handle.Version > 0 {
		_ = a.Provider.Terminate(r.Context(), req.Handle)
	}
	writeJSON(w, struct{}{})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, errors.New("tunnel: no body")
	}
	defer func() { _ = r.Body.Close() }()
	const maxBody = 1 << 20
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if len(buf) > maxBody {
			return nil, errors.New("tunnel: body too large")
		}
		if err != nil {
			break
		}
	}
	return buf, nil
}
