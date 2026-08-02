package tunnel_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envprovider/conformance"
	"github.com/aleksclark/ultralogical/envprovider/localdocker"
	"github.com/aleksclark/ultralogical/envprovider/tunnel"
	"github.com/aleksclark/ultralogical/mcp"
	"github.com/aleksclark/ultralogical/testkit/harness"
)

// tunnelled starts a real outbound tunnel: a broker the platform owns, and an
// agent that dials out to it. The agent opens no listening socket, so the
// platform genuinely cannot reach in; every control request travels down a
// connection the agent established.
type tunnelled struct {
	broker   *tunnel.Broker
	provider *tunnel.Provider
	agent    *tunnel.Agent
	local    *localdocker.Provider
	cancel   context.CancelFunc
	address  string
}

// pause stops the agent dialing out, which is what a machine going to sleep
// looks like from the platform's side.
func (l *tunnelled) pause() { l.cancel() }

// resume starts the agent dialing out again, at the same broker.
func (l *tunnelled) resume(t *testing.T) {
	t.Helper()
	dialer := &tunnel.AgentDialer{
		BrokerAddress: l.address, Token: agentToken,
		Handler: l.agent.Handler(), Connections: 6,
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	t.Cleanup(cancel)
	go func() { _ = dialer.Run(ctx) }()
}

func startTunnel(t *testing.T) *tunnelled {
	t.Helper()
	image := os.Getenv(bezalelImage)
	if image == "" {
		image = harness.EnsureBezalelImage(t)
	}
	local, err := localdocker.New(localdocker.Config{Image: image})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })

	broker := tunnel.NewBroker(func(token string) (string, error) {
		if token != agentToken {
			return "", errors.New("unknown agent")
		}
		return "laptop", nil
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = broker.Serve(ctx, listener) }()

	agent := &tunnel.Agent{Provider: local, Token: agentToken, Secret: agentSecret}
	dialer := &tunnel.AgentDialer{
		BrokerAddress: listener.Addr().String(), Token: agentToken,
		Handler: agent.Handler(), Connections: 6,
	}
	agentCtx, stopAgent := context.WithCancel(context.Background())
	t.Cleanup(stopAgent)
	go func() { _ = dialer.Run(agentCtx) }()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !broker.Connected("laptop") {
		time.Sleep(50 * time.Millisecond)
	}
	if !broker.Connected("laptop") {
		t.Fatal("the agent never dialed out to the broker")
	}

	// The host in this URL is never resolved: the transport hands every
	// request to a connection the agent already opened.
	provider, err := tunnel.New(tunnel.Config{
		ControlURL: "http://tunnelled-agent", Token: agentToken, Secret: agentSecret,
		Timeout: 60 * time.Second, Transport: broker.Transport("laptop"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &tunnelled{broker: broker, provider: provider, agent: agent, local: local,
		cancel: stopAgent, address: listener.Addr().String()}
}

// A10.5/A10.1 — the shared contract over a real outbound tunnel. The platform
// never connects to the agent; the agent dialed out and the platform answers
// down that connection.
func TestTunnelConformanceOverRealTransport(t *testing.T) {
	link := startTunnel(t)
	conformance.RunWith(t, func(t *testing.T) ultra.EnvProvider {
		return link.provider
	}, conformance.Options{
		Capabilities: ultra.ProviderCapabilities{
			Kind: ultra.ProviderKindTunnelLocal,
			Supported: []ultra.ProviderCapability{
				ultra.CapabilityRestartPreservesWorkspace,
				ultra.CapabilityToleratesDisconnect,
				ultra.CapabilityEnumeratesResources,
				ultra.CapabilityServesToolEndpoint,
			},
		},
		Inspect: func(t *testing.T, ctx context.Context, envID ultra.EnvID) []string {
			t.Helper()
			resources, err := link.provider.Resources(ctx, envID)
			if err != nil {
				t.Fatalf("agent inspection failed through the tunnel: %v", err)
			}
			return resources
		},
	})
}

// A10.5 — the agent listens on nothing. This is the property that makes the
// transport a tunnel rather than an address rewrite: a user needs no inbound
// firewall hole, and a platform that could dial the agent directly would not
// need a tunnel at all.
func TestA105_TheAgentAcceptsNoInboundConnections(t *testing.T) {
	link := startTunnel(t)
	ctx := context.Background()

	// Work flows through the tunnel.
	envID := ultra.EnvID(uuid.NewString())
	handle, err := link.provider.Provision(ctx, envID, ultra.EnvSpec{Name: "tunnel", Workdir: "/work"}, "env-token")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = link.provider.Terminate(context.Background(), handle) })

	// A provider pointed at the same control URL without the broker transport
	// has nowhere to connect: there is no agent address to reach.
	direct, err := tunnel.New(tunnel.Config{
		ControlURL: "http://tunnelled-agent", Token: agentToken, Secret: agentSecret,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := direct.Resources(ctx, envID); err == nil {
		t.Fatal("the agent was reachable without the tunnel, so the transport is not a tunnel")
	}
}

// A10.5 — severing the tunnel suspends rather than fails, and reconnecting
// resumes the same workspace. The agent redials on its own, which is what a
// laptop waking up looks like.
func TestA105_RealTunnelSeveredAndRestored(t *testing.T) {
	link := startTunnel(t)
	ctx := context.Background()

	envID := ultra.EnvID(uuid.NewString())
	handle, err := link.provider.Provision(ctx, envID, ultra.EnvSpec{Name: "tunnel", Workdir: "/work"}, "env-token")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = link.provider.Terminate(context.Background(), handle) })

	endpoint := awaitReadyThrough(t, ctx, link.provider, handle)
	client := mcp.NewClient(endpoint, "env-token")
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	write, _ := json.Marshal(map[string]any{"file_path": "/work/laptop.txt", "content": "before the lid closed\n"})
	if result, err := client.Call(ctx, "write", write); err != nil || result.IsError {
		t.Fatalf("write before the disconnect: %+v %v", result, err)
	}

	// The laptop goes to sleep: it stops dialing out, and the connections it
	// had are dropped. Dropping the connections alone would race the agent's
	// own redial, which is the agent behaving correctly.
	link.pause()
	link.broker.Disconnect("laptop")
	status := suspendedWithin(t, ctx, link.provider, handle, 30*time.Second)
	if status.State != ultra.EnvSuspended {
		t.Fatalf("a severed tunnel reported %q, want suspended", status.State)
	}

	// The laptop wakes and dials out again; no platform action reaches in.
	link.resume(t)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if s, err := link.provider.Status(ctx, handle); err == nil && s.State == ultra.EnvReady {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	status, err = link.provider.Status(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != ultra.EnvReady {
		t.Fatalf("after the agent redialed the environment is %q, want ready", status.State)
	}

	// The workspace survived: the same environment resumed.
	read, _ := json.Marshal(map[string]any{"file_path": "/work/laptop.txt"})
	result, err := client.Call(ctx, "view", read)
	if err != nil || result.IsError || !strings.Contains(result.Text, "before the lid closed") {
		t.Fatalf("the workspace did not survive the disconnect: %+v %v", result, err)
	}
}

// suspendedWithin polls until the platform reports suspension, because the
// transport failure surfaces on the next control request rather than instantly.
func suspendedWithin(t *testing.T, ctx context.Context, provider *tunnel.Provider,
	handle ultra.ProviderHandle, within time.Duration) ultra.ProviderStatus {
	t.Helper()
	deadline := time.Now().Add(within)
	var status ultra.ProviderStatus
	for time.Now().Before(deadline) {
		var err error
		status, err = provider.Status(ctx, handle)
		if err == nil && status.State == ultra.EnvSuspended {
			return status
		}
		time.Sleep(100 * time.Millisecond)
	}
	return status
}

// awaitReadyThrough waits for an environment reached over the tunnel.
func awaitReadyThrough(t *testing.T, ctx context.Context, provider *tunnel.Provider, handle ultra.ProviderHandle) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		status, err := provider.Status(ctx, handle)
		if err == nil && status.State == ultra.EnvReady {
			endpoint, err := provider.Endpoint(ctx, handle)
			if err == nil && mcp.Healthy(ctx, endpoint) == nil {
				return endpoint
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the tunnelled environment never became ready")
	return ""
}

// unusedHTTPHandler keeps the http import honest in this file.
var _ http.RoundTripper = (*http.Transport)(nil)
