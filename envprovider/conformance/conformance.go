// Package conformance is the black-box contract every environment provider
// must pass. It provisions real Bezalel and exercises the full contract:
// readiness, health, authenticated MCP discovery, shell execution, exact
// file editing, LSP, background jobs and per-call deadlines, token
// rejection, restart persistence with token rotation, terminate,
// idempotent repeat terminate, and resource leak checks.
package conformance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/mcp"
)

// Factory builds a provider for the suite.
type Factory func(t *testing.T) ultra.EnvProvider

// requiredTools are the Bezalel capabilities the agent loop depends on. A
// provider that cannot expose these cannot host a run.
var requiredTools = []string{"bash", "view", "write", "edit", "job_output", "lsp_diagnostics"}

// Run executes the complete provider contract.
func Run(t *testing.T, factory Factory) {
	ctx := context.Background()
	provider := factory(t)
	envID := ultra.EnvID(uuid.NewString())
	token := randomToken(t)
	spec := ultra.EnvSpec{Name: "conformance", Workdir: "/work"}

	var handle ultra.ProviderHandle
	var endpoint string
	var client *mcp.Client
	// Cleanup is registered on the parent test, not the Provision subtest, so
	// the environment survives for the rest of the contract.
	t.Cleanup(func() { _ = provider.Terminate(context.Background(), handle) })

	t.Run("Provision", func(t *testing.T) {
		h, err := provider.Provision(ctx, envID, spec, token)
		if err != nil {
			t.Fatal(err)
		}
		handle = h
		endpoint = awaitReady(t, ctx, provider, handle)
		if endpoint == "" {
			t.Fatal("provider published no endpoint")
		}
	})
	if endpoint == "" {
		t.Fatal("provisioning did not yield an endpoint; remaining contract cannot run")
	}

	t.Run("Health", func(t *testing.T) {
		if err := mcp.Healthy(ctx, endpoint); err != nil {
			t.Fatalf("health check on ready endpoint: %v", err)
		}
	})

	t.Run("Discovery", func(t *testing.T) {
		client = mcp.NewClient(endpoint, token)
		if err := client.Initialize(ctx); err != nil {
			t.Fatal(err)
		}
		discovered, err := client.Tools(ctx)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, tool := range discovered {
			names[tool.Name] = true
			if len(tool.InputSchema) == 0 {
				t.Fatalf("tool %q has no input schema", tool.Name)
			}
		}
		for _, want := range requiredTools {
			if !names[want] {
				t.Fatalf("authenticated discovery is missing required tool %q (got %v)", want, names)
			}
		}
	})
	if client == nil {
		t.Fatal("discovery failed; remaining contract cannot run")
	}

	call := func(t *testing.T, name string, args any) mcp.Result {
		t.Helper()
		b, _ := json.Marshal(args)
		result, err := client.Call(ctx, name, b)
		if err != nil || result.IsError {
			t.Fatalf("%s: result=%+v err=%v", name, result, err)
		}
		return result
	}

	t.Run("Bash", func(t *testing.T) {
		if got := call(t, "bash", map[string]any{"command": "echo hi"}).Text; got != "hi\n" {
			t.Fatalf("bash stdout = %q, want %q", got, "hi\n")
		}
		if got := call(t, "bash", map[string]any{"command": "printf 'a\\nb\\n' | wc -l"}).Text; !strings.Contains(got, "2") {
			t.Fatalf("bash pipeline output = %q, want it to contain 2", got)
		}
	})

	t.Run("ExactEdit", func(t *testing.T) {
		call(t, "write", map[string]any{"file_path": "/work/state.txt", "content": "before\nkeep\n"})
		if got := call(t, "view", map[string]any{"file_path": "/work/state.txt"}).Text; !strings.Contains(got, "before") {
			t.Fatalf("view after write = %q", got)
		}
		call(t, "edit", map[string]any{"file_path": "/work/state.txt", "old_string": "before", "new_string": "after"})
		got := call(t, "view", map[string]any{"file_path": "/work/state.txt"}).Text
		if !strings.Contains(got, "after") || strings.Contains(got, "before") {
			t.Fatalf("exact edit did not replace: %q", got)
		}
		if !strings.Contains(got, "keep") {
			t.Fatalf("exact edit clobbered untargeted content: %q", got)
		}
		// A non-matching edit must fail rather than silently no-op.
		b, _ := json.Marshal(map[string]any{"file_path": "/work/state.txt", "old_string": "not-present", "new_string": "x"})
		result, err := client.Call(ctx, "edit", b)
		if err == nil && !result.IsError {
			t.Fatalf("edit with a non-matching old_string succeeded: %+v", result)
		}
	})

	t.Run("LSP", func(t *testing.T) {
		// The LSP surface must answer structurally. Whether a language
		// server is configured in the image is a deployment choice, so a
		// typed unavailable result is acceptable; a transport failure or
		// an empty answer is not.
		b, _ := json.Marshal(map[string]any{})
		result, err := client.Call(ctx, "lsp_diagnostics", b)
		if err != nil {
			t.Fatalf("lsp_diagnostics transport failure: %v", err)
		}
		if strings.TrimSpace(result.Text) == "" {
			t.Fatal("lsp_diagnostics returned an empty result")
		}
	})

	t.Run("BackgroundJobAndTimeout", func(t *testing.T) {
		started := call(t, "bash", map[string]any{
			"command":           "sleep 1; echo background-done > /work/bg.txt",
			"run_in_background": true,
		})
		jobID := backgroundJobID(started.Text)
		if jobID == "" {
			t.Fatalf("background bash did not report a job id: %q", started.Text)
		}
		// job_output is addressable while the job runs and after it ends.
		if _, err := client.Call(ctx, "job_output", json.RawMessage(`{"job_id":"`+jobID+`"}`)); err != nil {
			t.Fatalf("job_output during run: %v", err)
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			b, _ := json.Marshal(map[string]any{"file_path": "/work/bg.txt"})
			if result, err := client.Call(ctx, "view", b); err == nil && !result.IsError &&
				strings.Contains(result.Text, "background-done") {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("background job never produced its output file")
	})

	t.Run("PerCallDeadline", func(t *testing.T) {
		// A caller-imposed deadline must abort the call rather than hang:
		// the loop relies on this to keep a wedged environment from
		// stalling a run forever.
		short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		start := time.Now()
		b, _ := json.Marshal(map[string]any{"command": "sleep 30"})
		if _, err := client.Call(short, "bash", b); err == nil {
			t.Fatal("a 30s command returned success under a 500ms deadline")
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("deadline took %s to take effect", elapsed)
		}
	})

	t.Run("TokenRejection", func(t *testing.T) {
		// /health is intentionally unauthenticated in Bezalel's contract;
		// the MCP endpoint is not.
		if err := mcp.NewClient(endpoint, "wrong-token").Initialize(ctx); err == nil {
			t.Fatal("wrong token authenticated")
		}
		if err := mcp.NewClient(endpoint, "").Initialize(ctx); err == nil {
			t.Fatal("missing token authenticated")
		}
	})

	newToken := randomToken(t)
	t.Run("RestartRotatesToken", func(t *testing.T) {
		newHandle, err := provider.Restart(ctx, envID, handle, spec, newToken)
		if err != nil {
			t.Fatal(err)
		}
		handle = newHandle
		endpoint = awaitReady(t, ctx, provider, handle)
		if err := mcp.NewClient(endpoint, token).Initialize(ctx); err == nil {
			t.Fatal("pre-restart token accepted after rotation")
		}
		rotated := mcp.NewClient(endpoint, newToken)
		if err := rotated.Initialize(ctx); err != nil {
			t.Fatalf("rotated token rejected: %v", err)
		}
		got, err := rotated.Call(ctx, "view", json.RawMessage(`{"file_path":"/work/state.txt"}`))
		if err != nil || got.IsError || !strings.Contains(got.Text, "after") {
			t.Fatalf("restart lost the workspace: %+v %v", got, err)
		}
		client = rotated
	})

	t.Run("Terminate", func(t *testing.T) {
		if err := provider.Terminate(ctx, handle); err != nil {
			t.Fatal(err)
		}
		if err := provider.Terminate(ctx, handle); err != nil {
			t.Fatalf("repeated terminate is not idempotent: %v", err)
		}
		// The endpoint must stop answering: the resource is really gone,
		// not merely marked terminated.
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if mcp.Healthy(ctx, endpoint) != nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("endpoint still healthy after terminate")
	})

	t.Run("LeakCheck", func(t *testing.T) {
		lister, ok := provider.(ultra.EnvResourceLister)
		if !ok {
			t.Skip("provider does not expose resource enumeration")
		}
		deadline := time.Now().Add(30 * time.Second)
		var leaked []string
		for time.Now().Before(deadline) {
			var err error
			leaked, err = lister.Resources(ctx, envID)
			if err != nil {
				t.Fatal(err)
			}
			if len(leaked) == 0 {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("terminated environment leaked resources: %v", leaked)
	})

	t.Run("ConcurrentProvisionDistinctEndpoints", func(t *testing.T) {
		type provisioned struct {
			id     ultra.EnvID
			handle ultra.ProviderHandle
		}
		var made []provisioned
		for range 3 {
			id := ultra.EnvID(uuid.NewString())
			h, err := provider.Provision(ctx, id, ultra.EnvSpec{Name: "concurrent", Workdir: "/work"}, randomToken(t))
			if err != nil {
				t.Fatal(err)
			}
			made = append(made, provisioned{id: id, handle: h})
		}
		t.Cleanup(func() {
			for _, p := range made {
				_ = provider.Terminate(context.Background(), p.handle)
			}
		})
		seen := map[string]bool{}
		for _, p := range made {
			ep := awaitReady(t, ctx, provider, p.handle)
			if seen[ep] {
				t.Fatalf("two environments share endpoint %s", ep)
			}
			seen[ep] = true
		}
	})
}

// backgroundJobID extracts the job identifier from a background-start reply.
func backgroundJobID(text string) string {
	_, rest, ok := strings.Cut(text, "ID:")
	if !ok {
		return ""
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], ".,")
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
