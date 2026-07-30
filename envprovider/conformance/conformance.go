// Package conformance is the black-box contract every environment provider
// must pass. It provisions real Bezalel, exercises authenticated MCP tools,
// verifies restart persistence/token rotation, and cleans up resources.
package conformance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/mcp"
)

// Factory builds a provider for the suite.
type Factory func(t *testing.T) ultra.EnvProvider

// Run executes the complete provider contract.
func Run(t *testing.T, factory Factory) {
	ctx := context.Background()
	provider := factory(t)
	envID := ultra.EnvID(uuid.NewString())
	token := randomToken(t)
	spec := ultra.EnvSpec{Name: "conformance", Workdir: "/work"}

	handle, err := provider.Provision(ctx, envID, spec, token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Terminate(context.Background(), handle) })

	endpoint := awaitReady(t, ctx, provider, handle)
	client := mcp.NewClient(endpoint, token)
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	// Wrong token is rejected at the MCP endpoint. /health is intentionally
	// unauthenticated in Bezalel's contract.
	wrong := mcp.NewClient(endpoint, "wrong-token")
	if err := wrong.Initialize(ctx); err == nil {
		t.Fatal("wrong token authenticated")
	}

	call := func(name string, args any) mcp.Result {
		t.Helper()
		b, _ := json.Marshal(args)
		result, err := client.Call(ctx, name, b)
		if err != nil || result.IsError {
			t.Fatalf("%s: result=%+v err=%v", name, result, err)
		}
		return result
	}
	if got := call("bash", map[string]any{"command": "echo hi"}).Text; got != "hi\n" {
		t.Fatalf("bash = %q", got)
	}
	call("write", map[string]any{"file_path": "/work/state.txt", "content": "before"})
	if got := call("view", map[string]any{"file_path": "/work/state.txt"}).Text; !contains(got, "before") {
		t.Fatalf("view before restart = %q", got)
	}

	// Restart preserves workspace and rotates authentication.
	newToken := randomToken(t)
	newHandle, err := provider.Restart(ctx, envID, handle, spec, newToken)
	if err != nil {
		t.Fatal(err)
	}
	handle = newHandle
	endpoint = awaitReady(t, ctx, provider, newHandle)
	if err := mcp.NewClient(endpoint, token).Initialize(ctx); err == nil {
		t.Fatal("old token accepted after restart")
	}
	newClient := mcp.NewClient(endpoint, newToken)
	if err := newClient.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := newClient.Call(ctx, "view", json.RawMessage(`{"file_path":"/work/state.txt"}`))
	if err != nil || !contains(got.Text, "before") {
		t.Fatalf("restart lost workspace: %+v %v", got, err)
	}

	if err := provider.Terminate(ctx, newHandle); err != nil {
		t.Fatal(err)
	}
	if err := provider.Terminate(ctx, newHandle); err != nil {
		t.Fatalf("double terminate: %v", err)
	}

}

func awaitReady(t *testing.T, ctx context.Context, provider ultra.EnvProvider, handle ultra.ProviderHandle) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
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
	t.Fatal("provider never became ready")
	return ""
}

func randomToken(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
