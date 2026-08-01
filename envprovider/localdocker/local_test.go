package localdocker_test

import (
	"context"
	"testing"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envprovider/conformance"
	"github.com/aleksclark/ultralogical/envprovider/localdocker"
	"github.com/aleksclark/ultralogical/testkit/harness"
)

// TestConformance runs the shared provider contract against real Docker.
//
// The capabilities declared here are the ones local Docker genuinely has, and
// the inspection callback reads Docker's own view of the world: a future
// change that made this adapter delegate its lifecycle elsewhere would fail
// here rather than passing on behavior alone.
func TestConformance(t *testing.T) {
	image := harness.EnsureBezalelImage(t)
	var provider *localdocker.Provider
	conformance.RunWith(t, func(t *testing.T) ultra.EnvProvider {
		created, err := localdocker.New(localdocker.Config{Image: image})
		if err != nil {
			t.Fatal(err)
		}
		provider = created
		t.Cleanup(func() { _ = created.Close() })
		return created
	}, conformance.Options{
		Capabilities: ultra.ProviderCapabilities{
			Kind: ultra.ProviderKindLocalDocker,
			Supported: []ultra.ProviderCapability{
				ultra.CapabilityRestartPreservesWorkspace,
				ultra.CapabilityAdoptsOrphans,
				ultra.CapabilityEnumeratesResources,
				ultra.CapabilityServesToolEndpoint,
			},
		},
		Inspect: func(t *testing.T, ctx context.Context, envID ultra.EnvID) []string {
			t.Helper()
			resources, err := provider.Resources(ctx, envID)
			if err != nil {
				t.Fatalf("docker inspection failed: %v", err)
			}
			return resources
		},
	})
}
