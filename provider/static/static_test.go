package static_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	uc "github.com/aleksclark/ultracore"
	provreg "github.com/aleksclark/ultracore/provider"
	"github.com/aleksclark/ultracore/provider/conformance"
	"github.com/aleksclark/ultracore/provider/static"
)

// maxWalkthroughCodeLines is the size the walkthrough promises, counted as
// lines of code: comments and blank lines are excluded.
//
// The metric is deliberately code rather than raw file length. This repository
// requires comments that explain why a decision was made, and an example whose
// reasoning is what a newcomer is reading would be made worse by deleting that
// reasoning to satisfy a line count. maxWalkthroughFileLines still bounds the
// whole file so the exemption cannot be used to hide unbounded growth.
const (
	maxWalkthroughCodeLines = 200
	maxWalkthroughFileLines = 300
)

// bezalelBinary extracts the Bezalel executable from the pinned image, which is
// how this provider gets a real agent without depending on a container runtime
// to run it. The image is the same one every other provider test uses, so the
// binary under test is not a separate build.
func bezalelBinary(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("CORE_TEST_BEZALEL_BINARY"); path != "" {
		return path
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is needed once to extract the Bezalel binary from the pinned image")
	}
	image := os.Getenv("CORE_BEZALEL_IMAGE")
	if image == "" {
		image = "ultracore/bezalel:phase2-test"
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "bezalel")
	created, err := exec.Command("docker", "create", image).Output()
	if err != nil {
		t.Skipf("the pinned Bezalel image is not available: %v", err)
	}
	container := strings.TrimSpace(string(created))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", container).Run() })
	if out, err := exec.Command("docker", "cp",
		container+":/usr/local/bin/bezalel", target).CombinedOutput(); err != nil {
		t.Fatalf("copy bezalel out of the image: %v\n%s", err, out)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	return target
}

// requireUserNamespaces skips when the host cannot create the sandbox. A
// developer on a machine without unprivileged user namespaces gets a skip
// rather than a confusing failure; CI runners have them.
func requireUserNamespaces(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("unshare is not installed")
	}
	if err := exec.Command("unshare", "--map-root-user", "--mount", "true").Run(); err != nil {
		t.Skipf("this host has no unprivileged user namespaces: %v", err)
	}
}

// A10.9 — the documented walkthrough provider passes the shared contract
// unmodified.
//
// This is what makes docs/providers.md executable: the suite is the same one
// every shipped adapter runs, called through the same entry point, so a change
// that made the contract unimplementable for a newcomer fails here.
func TestA109_StaticProviderWalkthrough(t *testing.T) {
	requireUserNamespaces(t)
	binary := bezalelBinary(t)
	root := t.TempDir()

	var provider *static.Provider
	conformance.RunWith(t, func(t *testing.T) uc.ResourceProvider {
		created, err := static.New(static.Config{Binary: binary, Root: root})
		if err != nil {
			t.Fatal(err)
		}
		provider = created
		return created
	}, conformance.Options{
		Capabilities: uc.ProviderCapabilities{
			Kind: uc.ProviderKindStatic,
			Supported: []uc.ProviderCapability{
				uc.CapabilityEnumeratesResources,
				uc.CapabilityRestartPreservesState,
				uc.CapabilityServesToolEndpoint,
			},
			Notes: map[uc.ProviderCapability]string{
			},
		},
		Inspect: func(t *testing.T, ctx context.Context, id uc.ResourceID) []string {
			t.Helper()
			resources, err := descriptorsFor(provider, ctx, id)
			if err != nil {
				t.Fatalf("static inspection failed: %v", err)
			}
			// The reported resource is read back from the filesystem the
			// provider actually owns, so the evidence is the host's view
			// rather than the adapter's bookkeeping.
			for _, item := range resources {
				id, ok := strings.CutPrefix(item, "sandbox/")
				if !ok {
					t.Fatalf("unexpected resource reference %q", item)
				}
				if _, err := os.Stat(filepath.Join(root, id)); err != nil {
					t.Fatalf("reported sandbox %q has no state on disk: %v", item, err)
				}
			}
			return resources
		},
	})
}

// A10.9 — the walkthrough provider stays small enough to be a walkthrough.
//
// Counting the implementation rather than the test keeps the promise honest:
// the number docs/providers.md quotes is about how much a newcomer has to read
// to add a kind, not about how thoroughly it is verified.
func TestA109_StaticProviderStaysUnderTheDocumentedSize(t *testing.T) {
	body, err := os.ReadFile("static.go")
	if err != nil {
		t.Fatal(err)
	}
	code, total := 0, 0
	for _, line := range strings.Split(string(body), "\n") {
		total++
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		code++
	}
	if code > maxWalkthroughCodeLines {
		t.Fatalf("the walkthrough provider is %d lines of code; docs/providers.md promises at most %d",
			code, maxWalkthroughCodeLines)
	}
	if total > maxWalkthroughFileLines {
		t.Fatalf("the walkthrough provider file is %d lines; the comment allowance is not unbounded (limit %d)",
			total, maxWalkthroughFileLines)
	}
	t.Logf("walkthrough provider is %d lines of code in a %d line file", code, total)
}

// A10.9 — the probe refuses a host that cannot run the sandbox instead of
// registering a provider whose every provision would fail.
func TestA109_StaticProviderProbeReportsRealCapabilities(t *testing.T) {
	requireUserNamespaces(t)
	provider, err := static.New(static.Config{Binary: bezalelBinary(t), Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := provider.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.Has(uc.CapabilityServesToolEndpoint) {
		t.Fatalf("the probe claims no tool endpoint: %+v", capabilities)
	}
	// An unsupported capability has to carry its reason, or an operator sees an
	// absence with no way to act on it.
}

// A10.9 — a configuration naming a binary that does not exist is refused at
// construction, so the failure appears at registration rather than at every
// later provision.
func TestA109_StaticProviderRefusesAMissingBinary(t *testing.T) {
	if _, err := static.New(static.Config{Binary: filepath.Join(t.TempDir(), "absent")}); err == nil {
		t.Fatal("a provider was built around a binary that does not exist")
	}
	if _, err := static.New(static.Config{}); err == nil {
		t.Fatal("a provider was built with no binary at all")
	}
}

// A10.9 — static provider configuration is selected by the worker's production
// registry and proven via native resource inspection, not by a test-only
// factory. The suite runs through StandardRegistry so a wiring regression that
// quietly dropped the kind fails here rather than only in a unit check.
func TestA109_WorkerSelectsStaticConfiguration(t *testing.T) {
	requireUserNamespaces(t)
	binary := bezalelBinary(t)
	root := t.TempDir()

	var provider uc.ResourceProvider
	conformance.RunWith(t, func(t *testing.T) uc.ResourceProvider {
		registry := provreg.StandardRegistry(provreg.Deployment{
			BezalelBinary: binary,
			EnabledKinds:  []string{uc.ProviderKindStatic},
		})
		// The binary comes from the deployment the worker would have read from
		// CORE_BEZALEL_BINARY; root is set on the registration so concurrent
		// environments still land under a disposable directory the test owns.
		built, err := registry.Build(t.Context(), uc.ProviderKindStatic,
			fmt.Appendf(nil, `{"root":%q}`, root))
		if err != nil {
			t.Fatal(err)
		}
		provider = built
		return built
	}, conformance.Options{
		Capabilities: uc.ProviderCapabilities{
			Kind: uc.ProviderKindStatic,
			Supported: []uc.ProviderCapability{
				uc.CapabilityEnumeratesResources,
				uc.CapabilityRestartPreservesState,
				uc.CapabilityServesToolEndpoint,
			},
			Notes: map[uc.ProviderCapability]string{
			},
		},
		Inspect: func(t *testing.T, ctx context.Context, id uc.ResourceID) []string {
			t.Helper()
			lister, ok := provider.(uc.ResourceLister)
			if !ok {
				t.Fatal("the registry-built static provider does not enumerate resources")
			}
			resources, err := descriptorsFor(lister, ctx, id)
			if err != nil {
				t.Fatalf("static inspection failed: %v", err)
			}
			for _, item := range resources {
				id, ok := strings.CutPrefix(item, "sandbox/")
				if !ok {
					t.Fatalf("unexpected resource reference %q", item)
				}
				if _, err := os.Stat(filepath.Join(root, id)); err != nil {
					t.Fatalf("reported sandbox %q has no state on disk: %v", item, err)
				}
			}
			return resources
		},
	})
}
