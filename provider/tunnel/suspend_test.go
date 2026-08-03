package tunnel_test

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/provider/localdocker"
	"github.com/aleksclark/ultracore/provider/tunnel"
	"github.com/aleksclark/ultracore/testkit/resourceconverge"
	"github.com/aleksclark/ultracore/testkit/harness"
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

	converge := resourceconverge.New(t, provider, resourceconverge.Options{
		Kind: uc.ProviderKindTunnelLocal, ReconcileInterval: 500 * time.Millisecond,
	})
	converge.Start(t)

	env := converge.Request(t, uc.DevEnvSpec{Name: "tunnel", Workdir: "/work"})
	t.Cleanup(func() {
		current, err := converge.Store.Org(converge.Org).Resources().Get(context.Background(), env.ID)
		if err == nil {
			_ = provider.Terminate(context.Background(), current)
		}
	})
	converge.Await(t, env.ID, uc.ResourceReady, 3*time.Minute)

	// The user's machine goes offline.
	server.CloseClientConnections()
	server.Close()

	// The platform must record suspension, not failure: the workspace still
	// exists on a machine that will come back.
	converge.Await(t, env.ID, uc.ResourceSuspended, 60*time.Second)

	// Metering stops while the host is away. A user is not billed, even at
	// their own rate, for a laptop that is closed.

	// The machine comes back at the same address, which is what reconnecting
	// is, and the environment resumes rather than being rebuilt.
	restored := restoreAt(t, server.URL, agent.Handler())
	t.Cleanup(restored.Close)
	converge.Await(t, env.ID, uc.ResourceReady, 90*time.Second)

	// The workspace survived, so this is the same environment resuming.
	current, err := converge.Store.Org(converge.Org).Resources().Get(context.Background(), env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ReadyAt == nil {
		t.Fatal("a resumed environment reports no ready time")
	}
}

