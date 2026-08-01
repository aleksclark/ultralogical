package tunnel_test

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envprovider/localdocker"
	"github.com/aleksclark/ultralogical/envprovider/tunnel"
	"github.com/aleksclark/ultralogical/testkit/envconverge"
	"github.com/aleksclark/ultralogical/testkit/harness"
)

// A10.5 — a lost transport must suspend the environment rather than fail it,
// end to end through the durable lifecycle.
//
// The adapter's own answer is already asserted by
// TestA105_DisconnectSuspendsAndReconnectResumes. This test asks the different
// and more consequential question: what does the *platform* record. A user
// whose laptop closes its lid has intact work on a machine that is merely
// offline, so an environment marked failed is a destroyed workspace as far as
// every other surface is concerned.
func TestA105_LostTransportSuspendsRatherThanFails(t *testing.T) {
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
	server := httptest.NewServer(agent.Handler())
	provider := platform(t, server.URL)

	converge := envconverge.New(t, provider, envconverge.Options{
		Kind: ultra.ProviderKindTunnelLocal, ReconcileInterval: 500 * time.Millisecond,
	})
	converge.Start(t)

	env := converge.Request(t, ultra.EnvSpec{Name: "tunnel", Workdir: "/work"})
	t.Cleanup(func() {
		current, err := converge.Store.Org(converge.Org).Envs().Get(context.Background(), env.ID)
		if err == nil {
			_ = provider.Terminate(context.Background(), current.Handle)
		}
	})
	converge.Await(t, env.ID, ultra.EnvReady, 3*time.Minute)

	// The user's machine goes offline.
	server.CloseClientConnections()
	server.Close()

	// The platform must record suspension, not failure: the workspace still
	// exists on a machine that will come back.
	converge.Await(t, env.ID, ultra.EnvSuspended, 60*time.Second)

	// Metering stops while the host is away. A user is not billed, even at
	// their own rate, for a laptop that is closed.
	suspendedFor := openUsageIntervals(t, converge)
	if suspendedFor != 0 {
		t.Fatalf("a suspended environment still has %d open metering interval(s)", suspendedFor)
	}

	// The machine comes back at the same address, which is what reconnecting
	// is, and the environment resumes rather than being rebuilt.
	restored := restoreAt(t, server.URL, agent.Handler())
	t.Cleanup(restored.Close)
	converge.Await(t, env.ID, ultra.EnvReady, 90*time.Second)

	// The workspace survived, so this is the same environment resuming.
	current, err := converge.Store.Org(converge.Org).Envs().Get(context.Background(), env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ReadyAt == nil {
		t.Fatal("a resumed environment reports no ready time")
	}
	if resumed := openUsageIntervals(t, converge); resumed != 1 {
		t.Fatalf("a resumed environment has %d open metering interval(s), want 1", resumed)
	}
}

// openUsageIntervals counts the environment meters currently running, which is
// how "billing paused" becomes a checkable claim rather than an intention.
func openUsageIntervals(t *testing.T, converge *envconverge.Harness) int {
	t.Helper()
	open, err := converge.Store.Org(converge.Org).Usage().ListOpen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(open)
}
