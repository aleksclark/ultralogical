// Package conformance is the black-box contract every resource provider
// must pass. Core lifecycle checks apply to every kind. Tool-surface checks
// (Discovery, Bash, ExactEdit, LSP, background jobs, deadlines) apply only
// to kinds that serve an authenticated tool endpoint.
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

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/mcp"
)

// Factory builds a provider for the suite.
type Factory func(t *testing.T) uc.ResourceProvider

// requiredTools are the Bezalel capabilities the agent loop depends on.
var requiredTools = []string{"bash", "view", "write", "edit", "job_output", "lsp_diagnostics"}

// Options parameterize a conformance run.
//
// Capabilities change *how* a behavior is verified, never *whether* it is
// (within the applicable contract). SkipToolSurface forces core-only checks
// even when ServesToolEndpoint is claimed.
type Options struct {
	Capabilities    uc.ProviderCapabilities
	Inspect         func(t *testing.T, ctx context.Context, id uc.ResourceID) []string
	SkipToolSurface bool
}

// Run executes the complete provider contract with no optional capabilities
// claimed. Real tool-serving providers should prefer RunWith and pass their
// probed capabilities.
func Run(t *testing.T, factory Factory) {
	RunWith(t, factory, Options{})
}

// RunWith executes the provider contract under the given options.
func RunWith(t *testing.T, factory Factory, options Options) {
	assertCapabilitiesCannotWaiveCore(t, options.Capabilities)
	ctx := context.Background()
	provider := factory(t)
	id := uc.ResourceID(uuid.NewString())
	token := randomToken(t)
	resource := testResource(provider, id)

	var handle json.RawMessage
	var endpoint uc.ToolEndpoint
	var client *mcp.Client
	servesTools := !options.SkipToolSurface && (
		options.Capabilities.Has(uc.CapabilityServesToolEndpoint) ||
			// Empty capabilities on a real tool provider: default to running
			// tool surface so a misconfigured options struct cannot weaken
			// the suite for dev_env adapters.
			(len(options.Capabilities.Supported) == 0 && provider.Kind() == uc.ResourceKindDevEnv) ||
			provider.Kind() == uc.ResourceKindDevEnv && !options.SkipToolSurface)

	// Lifecycle-only kinds never run tool surface.
	if provider.Kind() == uc.ResourceKindNullResource || options.SkipToolSurface {
		servesTools = false
	}
	if options.Capabilities.Has(uc.CapabilityServesToolEndpoint) && !options.SkipToolSurface {
		servesTools = true
	}

	t.Cleanup(func() {
		r := resource
		r.Handle = handle
		_ = provider.Terminate(context.Background(), r)
	})

	t.Run("Provision", func(t *testing.T) {
		h, err := provider.Provision(ctx, resource, token)
		if err != nil {
			t.Fatal(err)
		}
		handle = h
		resource.Handle = h
		endpoint = awaitReady(t, ctx, provider, resource, servesTools)
		if servesTools && endpoint == "" {
			t.Fatal("provider published no endpoint")
		}
		resource.Endpoint = endpoint
	})
	if !uc.HandlePresent(handle) {
		t.Fatal("provisioning did not yield a handle; remaining contract cannot run")
	}
	if servesTools && endpoint == "" {
		t.Fatal("provisioning did not yield an endpoint; remaining contract cannot run")
	}

	t.Run("ProviderNativeResources", func(t *testing.T) {
		if options.Inspect == nil {
			t.Fatal("conformance requires provider-native inspection; without it a delegating alias passes")
		}
		found := options.Inspect(t, ctx, id)
		if len(found) == 0 {
			t.Fatal("the provider reported no native resources for a provisioned resource")
		}
		t.Logf("provider-native resources: %v", found)
	})

	t.Run("Health", func(t *testing.T) {
		if err := provider.HealthCheck(ctx, resource); err != nil {
			// Fall back to Status ready for providers that only implement Status.
			st, stErr := provider.Status(ctx, resource)
			if stErr != nil || st.State != uc.ResourceReady {
				t.Fatalf("health check: %v (status=%v %v)", err, st, stErr)
			}
		}
		if servesTools && endpoint != "" {
			if err := mcp.Healthy(ctx, string(endpoint)); err != nil {
				t.Fatalf("health check on ready endpoint: %v", err)
			}
		}
	})

	if servesTools {
		t.Run("Discovery", func(t *testing.T) {
			client = mcp.NewClient(string(endpoint), token)
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
			if got := call(t, "bash", map[string]any{"command": "printf 'a\nb\n' | wc -l"}).Text; !strings.Contains(got, "2") {
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
			b, _ := json.Marshal(map[string]any{"file_path": "/work/state.txt", "old_string": "not-present", "new_string": "x"})
			result, err := client.Call(ctx, "edit", b)
			if err == nil && !result.IsError {
				t.Fatalf("edit with a non-matching old_string succeeded: %+v", result)
			}
		})

		t.Run("LSP", func(t *testing.T) {
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
	}

	if servesTools {
		t.Run("TokenRejection", func(t *testing.T) {
			if err := mcp.NewClient(string(endpoint), "wrong-token").Initialize(ctx); err == nil {
				t.Fatal("wrong token authenticated")
			}
			if err := mcp.NewClient(string(endpoint), "").Initialize(ctx); err == nil {
				t.Fatal("missing token authenticated")
			}
		})
	} else {
		t.Run("TokenRejection", func(t *testing.T) {
			t.Skip("lifecycle-only resource has no token-authenticated endpoint")
		})
	}

	newToken := randomToken(t)
	t.Run("RestartRotatesToken", func(t *testing.T) {
		newHandle, err := provider.Restart(ctx, resource, newToken)
		if err != nil {
			t.Fatal(err)
		}
		handle = newHandle
		resource.Handle = newHandle
		endpoint = awaitReady(t, ctx, provider, resource, servesTools)
		resource.Endpoint = endpoint
		if servesTools {
			if err := mcp.NewClient(string(endpoint), token).Initialize(ctx); err == nil {
				t.Fatal("pre-restart token accepted after rotation")
			}
			rotated := mcp.NewClient(string(endpoint), newToken)
			if err := rotated.Initialize(ctx); err != nil {
				t.Fatalf("rotated token rejected: %v", err)
			}
			got, err := rotated.Call(ctx, "view", json.RawMessage(`{"file_path":"/work/state.txt"}`))
			if options.Capabilities.Has(uc.CapabilityRestartPreservesState) {
				if err != nil || got.IsError || !strings.Contains(got.Text, "after") {
					t.Fatalf("provider claims restart_preserves_state but restart lost the workspace: %+v %v", got, err)
				}
			} else {
				probe, probeErr := rotated.Call(ctx, "bash", json.RawMessage(`{"command":"echo restarted"}`))
				if probeErr != nil || probe.IsError || !strings.Contains(probe.Text, "restarted") {
					t.Fatalf("restart did not yield a working resource: %+v %v", probe, probeErr)
				}
			}
			client = rotated
		} else {
			st, err := provider.Status(ctx, resource)
			if err != nil || st.State != uc.ResourceReady {
				t.Fatalf("restart did not yield ready status: %+v %v", st, err)
			}
		}
	})

	t.Run("Terminate", func(t *testing.T) {
		if err := provider.Terminate(ctx, resource); err != nil {
			t.Fatal(err)
		}
		if err := provider.Terminate(ctx, resource); err != nil {
			t.Fatalf("repeated terminate is not idempotent: %v", err)
		}
		if servesTools && endpoint != "" {
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				if mcp.Healthy(ctx, string(endpoint)) != nil {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatal("endpoint still healthy after terminate")
		}
	})

	t.Run("LeakCheck", func(t *testing.T) {
		lister, ok := provider.(uc.ResourceLister)
		claimed := options.Capabilities.Has(uc.CapabilityEnumeratesResources)
		if claimed && !ok {
			t.Fatal("provider claims enumerates_resources but implements no resource lister")
		}
		if !ok {
			if options.Inspect == nil {
				t.Fatal("a provider without resource enumeration must still support native inspection")
			}
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				if len(options.Inspect(t, ctx, id)) == 0 {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatal("native inspection still reports resources after terminate")
		}
		deadline := time.Now().Add(30 * time.Second)
		var leaked []string
		for time.Now().Before(deadline) {
			owned, err := lister.ListOwned(ctx)
			if err != nil {
				t.Fatal(err)
			}
			leaked = nil
			for _, o := range owned {
				if o.ResourceID == id {
					leaked = append(leaked, o.Descriptors...)
				}
			}
			if len(leaked) == 0 {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("terminated resource leaked resources: %v", leaked)
	})

	t.Run("ConcurrentProvisionDistinctEndpoints", func(t *testing.T) {
		type provisioned struct {
			r      uc.Resource
			handle json.RawMessage
		}
		var made []provisioned
		for range 3 {
			rid := uc.ResourceID(uuid.NewString())
			res := testResource(provider, rid)
			h, err := provider.Provision(ctx, res, randomToken(t))
			if err != nil {
				t.Fatal(err)
			}
			res.Handle = h
			made = append(made, provisioned{r: res, handle: h})
		}
		t.Cleanup(func() {
			for _, p := range made {
				_ = provider.Terminate(context.Background(), p.r)
			}
		})
		if servesTools {
			seen := map[string]bool{}
			for _, p := range made {
				ep := awaitReady(t, ctx, provider, p.r, true)
				if seen[string(ep)] {
					t.Fatalf("two resources share endpoint %s", ep)
				}
				seen[string(ep)] = true
			}
		} else {
			// Distinct handles/ids is enough for lifecycle-only kinds.
			seen := map[string]bool{}
			for _, p := range made {
				key := string(p.handle)
				if seen[key] {
					t.Fatal("two concurrent provisions produced identical handles")
				}
				seen[key] = true
			}
		}
	})
}

func testResource(provider uc.ResourceProvider, id uc.ResourceID) uc.Resource {
	kind := provider.Kind()
	var spec json.RawMessage
	if kind == uc.ResourceKindNullResource {
		spec = json.RawMessage(`{"name":"conformance"}`)
	} else {
		b, _ := json.Marshal(uc.DevEnvSpec{Name: "conformance", Workdir: "/work"})
		spec = b
		if kind == "" {
			kind = uc.ResourceKindDevEnv
		}
	}
	return uc.Resource{ID: id, Kind: kind, Spec: spec, State: uc.ResourceRequested}
}

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

func awaitReady(t *testing.T, ctx context.Context, provider uc.ResourceProvider, r uc.Resource, requireEndpoint bool) uc.ToolEndpoint {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		status, err := provider.Status(ctx, r)
		if err == nil && status.State == uc.ResourceReady {
			endpoint, err := provider.Endpoint(ctx, r)
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			if requireEndpoint {
				if endpoint != "" && mcp.Healthy(ctx, string(endpoint)) == nil {
					return endpoint
				}
			} else {
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

// assertCapabilitiesCannotWaiveCore rejects a manifest that tries to make a
// core behavior optional.
func assertCapabilitiesCannotWaiveCore(t *testing.T, capabilities uc.ProviderCapabilities) {
	t.Helper()
	core := map[string]bool{}
	for _, name := range uc.CoreProviderContract() {
		core[strings.ToLower(name)] = true
	}
	for _, capability := range capabilities.Supported {
		if core[strings.ToLower(string(capability))] {
			t.Fatalf("capability %q names a core contract behavior; core behaviors are never optional", capability)
		}
	}
}
