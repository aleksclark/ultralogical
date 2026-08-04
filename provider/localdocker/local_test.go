package localdocker_test

import (
	"context"
	"testing"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/provider/conformance"
	"github.com/aleksclark/ultracore/provider/localdocker"
	"github.com/aleksclark/ultracore/testkit/harness"
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
	conformance.RunWith(t, func(t *testing.T) uc.ResourceProvider {
		created, err := localdocker.New(localdocker.Config{Image: image})
		if err != nil {
			t.Fatal(err)
		}
		provider = created
		t.Cleanup(func() { _ = created.Close() })
		return created
	}, conformance.Options{
		Capabilities: uc.ProviderCapabilities{
			Kind: uc.ProviderKindLocalDocker,
			Supported: []uc.ProviderCapability{
				uc.CapabilityRestartPreservesState,
				uc.CapabilityAdoptsOrphans,
				uc.CapabilityEnumeratesResources,
				uc.CapabilityServesToolEndpoint,
			},
		},
		Inspect: func(t *testing.T, ctx context.Context, id uc.ResourceID) []string {
			t.Helper()
			resources, err := descriptorsFor(provider, ctx, id)
			if err != nil {
				t.Fatalf("docker inspection failed: %v", err)
			}
			return resources
		},
	})
}
