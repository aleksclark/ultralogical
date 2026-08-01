package envprovider_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envprovider"
)

// A10.11 anti-alias rule — no provider adapter may delegate its lifecycle to
// local Docker. The tunnel agent is the one legitimate exception: it runs on
// the user's own machine, which is exactly where their Docker is.
func TestNoProviderAliasesToLocalDocker(t *testing.T) {
	const dockerPackage = "github.com/aleksclark/ultralogical/envprovider/localdocker"
	allowed := map[string]bool{
		// The adapter that is local Docker.
		filepath.Join("localdocker", "local.go"): true,
		// The user-side agent, which owns their machine's Docker. The
		// platform-side provider in the same package must not.
		filepath.Join("tunnel", "agent.go"): true,
		// Deployment wiring names every adapter, including this one.
		"wiring.go": true,
	}
	root := "."
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative := strings.TrimPrefix(path, "./")
		if allowed[relative] {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imported := range file.Imports {
			if strings.Trim(imported.Path.Value, `"`) == dockerPackage {
				t.Errorf("%s imports local Docker; a provider kind must drive its own control plane", relative)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A10.1 — every kind a deployment can offer resolves to a distinct adapter.
// A registry that returned the same implementation for two kinds would be the
// alias problem wearing a different name.
func TestStandardRegistryOffersEveryKind(t *testing.T) {
	registry := envprovider.StandardRegistry(envprovider.Deployment{BezalelImage: "test"})
	for _, kind := range []string{
		ultra.ProviderKindLocalDocker, ultra.ProviderKindBYOKubernetes,
		ultra.ProviderKindHostedEKS, ultra.ProviderKindBYONomad, ultra.ProviderKindTunnelLocal,
	} {
		if !registry.Enabled(kind) {
			t.Errorf("the standard registry does not offer %q", kind)
		}
	}
	// A deployment can narrow what it hosts, and a kind it did not enable is
	// refused rather than quietly substituted.
	narrow := envprovider.StandardRegistry(envprovider.Deployment{
		BezalelImage: "test", EnabledKinds: []string{ultra.ProviderKindLocalDocker},
	})
	if narrow.Enabled(ultra.ProviderKindBYOKubernetes) {
		t.Error("a narrowed deployment still offers Kubernetes")
	}
	if !narrow.Enabled(ultra.ProviderKindLocalDocker) {
		t.Error("a narrowed deployment dropped the kind it enabled")
	}
}

// A10.6 — an unknown configuration field is a typo an operator must see, not a
// setting to ignore. Silently accepting it would let a misspelled namespace
// send environments somewhere unexpected.
func TestRegistryRejectsUnknownConfigurationFields(t *testing.T) {
	registry := envprovider.StandardRegistry(envprovider.Deployment{BezalelImage: "test"})
	_, err := registry.Build(t.Context(), ultra.ProviderKindBYOKubernetes,
		[]byte(`{"namespcae":"typo"}`))
	if err == nil {
		t.Fatal("a misspelled configuration field was accepted")
	}
	if !strings.Contains(err.Error(), "namespcae") {
		t.Fatalf("the error does not name the offending field: %v", err)
	}
}

// A10.6 — a kind this deployment does not host is refused by name rather than
// falling back to something that happens to be available.
func TestRegistryRefusesDisabledKinds(t *testing.T) {
	registry := envprovider.StandardRegistry(envprovider.Deployment{
		BezalelImage: "test", EnabledKinds: []string{ultra.ProviderKindLocalDocker},
	})
	if _, err := registry.Build(t.Context(), ultra.ProviderKindBYONomad, nil); err == nil {
		t.Fatal("a disabled kind was built")
	}
}
