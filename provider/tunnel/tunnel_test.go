package tunnel_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/provider/conformance"
	"github.com/aleksclark/ultracore/provider/localdocker"
	"github.com/aleksclark/ultracore/provider/tunnel"
	"github.com/aleksclark/ultracore/mcp"
	"github.com/aleksclark/ultracore/testkit/harness"
)

const (
	agentToken   = "tunnel-registration-token"
	agentSecret  = "tunnel-signing-secret"
	bezalelImage = "CORE_BEZALEL_IMAGE"
)

// startAgent runs the real agent over a real HTTP transport, exactly as the
// shipped binary does. The tunnel between platform and agent is a loopback
// listener here rather than cloudflared, but every request still crosses the
// network and carries the same authentication and signature.
func startAgent(t *testing.T) (*tunnel.Agent, string) {
	t.Helper()
	image := os.Getenv(bezalelImage)
	if image == "" {
		image = harness.EnsureBezalelImage(t)
	}
	provider, err := localdocker.New(localdocker.Config{Image: image})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	agent := &tunnel.Agent{Provider: provider, Token: agentToken, Secret: agentSecret}
	server := httptest.NewServer(agent.Handler())
	t.Cleanup(server.Close)
	return agent, server.URL
}

func platform(t *testing.T, controlURL string) *tunnel.Provider {
	t.Helper()
	provider, err := tunnel.New(tunnel.Config{
		ControlURL: controlURL, Token: agentToken, Secret: agentSecret, Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// A10.1/A10.5 — the shared provider contract driven entirely through the
// agent's control API. Inspection asks the agent what it holds, so the
// evidence comes from the machine that actually runs the environment.
func TestTunnelConformance(t *testing.T) {
	_, controlURL := startAgent(t)
	var provider *tunnel.Provider
	conformance.RunWith(t, func(t *testing.T) uc.ResourceProvider {
		provider = platform(t, controlURL)
		return provider
	}, conformance.Options{
		Capabilities: uc.ProviderCapabilities{
			Kind: uc.ProviderKindTunnelLocal,
			Supported: []uc.ProviderCapability{
				uc.CapabilityRestartPreservesState,
				uc.CapabilityToleratesDisconnect,
				uc.CapabilityEnumeratesResources,
				uc.CapabilityServesToolEndpoint,
			},
		},
		Inspect: func(t *testing.T, ctx context.Context, id uc.ResourceID) []string {
			t.Helper()
			resources, err := descriptorsFor(provider, ctx, id)
			if err != nil {
				t.Fatalf("agent inspection failed: %v", err)
			}
			return resources
		},
	})
}

// A10.5 — the agent refuses a caller that has the tunnel URL but cannot sign,
// so a leaked URL is not remote execution on the user's machine.
func TestA105_UnsignedControlRequestsAreRefused(t *testing.T) {
	_, controlURL := startAgent(t)
	body, err := json.Marshal(tunnel.ProvisionRequest{
		ResourceID: uc.ResourceID(uuid.NewString()),
		Spec:  uc.DevEnvSpec{Name: "forged", Workdir: "/work"},
		Token: "forged",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Correct token, no signature.
	req, err := http.NewRequest(http.MethodPost, controlURL+tunnel.PathProvision, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+agentToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an unsigned control request returned HTTP %d, want 403", resp.StatusCode)
	}

	// Correct signature, wrong token.
	timestamp := time.Now()
	req, err = http.NewRequest(http.MethodPost, controlURL+tunnel.PathProvision, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wrong-token")
	req.Header.Set(tunnel.HeaderTimestamp, timestampHeader(timestamp))
	req.Header.Set(tunnel.HeaderSignature, tunnel.Sign(agentSecret, tunnel.PathProvision, timestamp, body))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request with the wrong token returned HTTP %d, want 401", resp.StatusCode)
	}

	// A signature for a different path must not authorize this one.
	req, err = http.NewRequest(http.MethodPost, controlURL+tunnel.PathProvision, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+agentToken)
	req.Header.Set(tunnel.HeaderTimestamp, timestampHeader(timestamp))
	req.Header.Set(tunnel.HeaderSignature, tunnel.Sign(agentSecret, tunnel.PathTerminate, timestamp, body))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a signature for another operation returned HTTP %d, want 403", resp.StatusCode)
	}

	// A stale signature must stop working, so a captured request is not
	// replayable forever.
	stale := time.Now().Add(-2 * tunnel.SignatureWindow)
	req, err = http.NewRequest(http.MethodPost, controlURL+tunnel.PathProvision, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+agentToken)
	req.Header.Set(tunnel.HeaderTimestamp, timestampHeader(stale))
	req.Header.Set(tunnel.HeaderSignature, tunnel.Sign(agentSecret, tunnel.PathProvision, stale, body))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a stale signed request returned HTTP %d, want 403", resp.StatusCode)
	}
}

func timestampHeader(at time.Time) string { return strconv.FormatInt(at.Unix(), 10) }

// A10.5 — losing the transport suspends the environment rather than failing
// it, and restoring it resumes the same workspace. A user closing a laptop
// must not destroy their work.
func TestA105_DisconnectSuspendsAndReconnectResumes(t *testing.T) {
	image := os.Getenv(bezalelImage)
	if image == "" {
		image = harness.EnsureBezalelImage(t)
	}
	local, err := localdocker.New(localdocker.Config{Image: image})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	agent := &tunnel.Agent{Provider: local, Token: agentToken, Secret: agentSecret}

	// The tunnel is a listener the test can sever and restore, which is what a
	// laptop going offline looks like from the platform's side.
	server := httptest.NewServer(agent.Handler())
	controlURL := server.URL
	provider := platform(t, controlURL)

	ctx := context.Background()
	envID := uc.ResourceID(uuid.NewString())
	handle, err := provider.Provision(ctx, testRes(envID, uc.DevEnvSpec{Name: "tunnel", Workdir: "/work"}), "env-token")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Terminate(context.Background(), uc.Resource{Handle: handle}) })

	endpoint := awaitReady(t, ctx, provider, handle)
	client := mcp.NewClient(endpoint, "env-token")
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	write, _ := json.Marshal(map[string]any{"file_path": "/work/laptop.txt", "content": "written before the disconnect\n"})
	if result, err := client.Call(ctx, "write", write); err != nil || result.IsError {
		t.Fatalf("write before disconnect: %+v %v", result, err)
	}

	// Sever the tunnel. The platform must report suspended, not failed.
	server.CloseClientConnections()
	server.Close()
	status, err := provider.Status(ctx, uc.Resource{Handle: handle})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != uc.ResourceSuspended {
		t.Fatalf("a severed tunnel reported %q, want suspended: a disconnected laptop must not destroy work", status.State)
	}

	// Restore the tunnel at the same address, which is what reconnecting is.
	restored := restoreAt(t, controlURL, agent.Handler())
	t.Cleanup(restored.Close)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if status, err := provider.Status(ctx, uc.Resource{Handle: handle}); err == nil && status.State == uc.ResourceReady {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	status, err = provider.Status(ctx, uc.Resource{Handle: handle})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != uc.ResourceReady {
		t.Fatalf("after reconnecting the environment is %q, want ready", status.State)
	}

	// The workspace survived: this is the same environment, not a new one.
	resumed := mcp.NewClient(endpoint, "env-token")
	if err := resumed.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	read, _ := json.Marshal(map[string]any{"file_path": "/work/laptop.txt"})
	result, err := resumed.Call(ctx, "view", read)
	if err != nil || result.IsError || !strings.Contains(result.Text, "written before the disconnect") {
		t.Fatalf("the workspace did not survive the disconnect: %+v %v", result, err)
	}

	// Revoking the lease releases the environment and makes the agent refuse
	// further work, so a user withdrawing access really withdraws it.
	if err := provider.RevokeLease(ctx, handle); err != nil {
		t.Fatal(err)
	}
	if !agent.Revoked() {
		t.Fatal("the agent did not record the revocation")
	}
	if _, err := provider.Provision(ctx, testRes(uc.ResourceID(uuid.NewString()), uc.DevEnvSpec{Name: "after-revoke", Workdir: "/work"}), "token"); err == nil {
		t.Fatal("a revoked agent accepted new work")
	}
	gone := time.Now().Add(30 * time.Second)
	for time.Now().Before(gone) {
		if mcp.Healthy(ctx, string(endpoint)) != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the environment still answers after its lease was revoked")
}

// restoreAt rebuilds a listener on the address the tunnel previously used.
func restoreAt(t *testing.T, controlURL string, handler http.Handler) *httptest.Server {
	t.Helper()
	address := strings.TrimPrefix(controlURL, "http://")
	listener, err := listenOn(address)
	if err != nil {
		t.Fatalf("could not restore the tunnel at %s: %v", address, err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}}
	server.Start()
	return server
}

func awaitReady(t *testing.T, ctx context.Context, provider *tunnel.Provider, handle json.RawMessage) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		status, err := provider.Status(ctx, uc.Resource{Handle: handle})
		if err == nil && status.State == uc.ResourceReady {
			endpoint, err := provider.Endpoint(ctx, uc.Resource{Handle: handle})
			if err == nil && mcp.Healthy(ctx, string(endpoint)) == nil {
				return string(endpoint)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the tunnelled environment never became ready")
	return ""
}

// listenOn binds a specific address, so a severed tunnel can be restored where
// the platform still expects to find it.
func listenOn(address string) (net.Listener, error) { return net.Listen("tcp", address) }
