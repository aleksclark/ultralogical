package nomad_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/envprovider/conformance"
	"github.com/aleksclark/ultracore/envprovider/nomad"
	"github.com/aleksclark/ultracore/testkit/harness"
)

// nomadAddress returns the dev agent's address, skipping when none is running.
func nomadAddress(t *testing.T) string {
	t.Helper()
	address := os.Getenv("NOMAD_ADDR")
	if address == "" {
		address = "http://127.0.0.1:4646"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(address + "/v1/agent/health")
	if err != nil {
		t.Skipf("no Nomad agent at %s", address)
	}
	_ = resp.Body.Close()
	return address
}

func clusterImage(t *testing.T) string {
	t.Helper()
	if image := os.Getenv("CORE_BEZALEL_IMAGE"); image != "" {
		return image
	}
	return harness.BezalelImage
}

// A10.1/A10.4 — the shared provider contract against a real Nomad agent, with
// inspection reading Nomad's own jobs and allocations. An adapter that
// delegated its lifecycle elsewhere would pass every behavioral assertion and
// still fail here.
func TestNomadConformance(t *testing.T) {
	address := nomadAddress(t)
	var provider *nomad.Provider
	conformance.RunWith(t, func(t *testing.T) uc.EnvProvider {
		created, err := nomad.New(nomad.Config{
			Address: address, Image: clusterImage(t), EndpointHost: "127.0.0.1",
		})
		if err != nil {
			t.Fatal(err)
		}
		provider = created
		return created
	}, conformance.Options{
		Capabilities: uc.ProviderCapabilities{
			Kind: uc.ProviderKindBYONomad,
			Supported: []uc.ProviderCapability{
				uc.CapabilityAdoptsOrphans,
				uc.CapabilityEnumeratesResources,
				uc.CapabilityServesToolEndpoint,
			},
			Notes: map[uc.ProviderCapability]string{
				uc.CapabilityRestartPreservesWorkspace: "a replacement allocation receives a fresh allocation directory",
			},
		},
		Inspect: func(t *testing.T, ctx context.Context, envID uc.EnvID) []string {
			t.Helper()
			resources, err := provider.Resources(ctx, envID)
			if err != nil {
				t.Fatalf("nomad inspection failed: %v", err)
			}
			// Read the job back through Nomad's own API, so the evidence is
			// the cluster's view rather than the adapter's bookkeeping.
			client, err := nomadapi.NewClient(&nomadapi.Config{Address: address})
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range resources {
				name, ok := strings.CutPrefix(item, "job/")
				if !ok {
					continue
				}
				job, _, err := client.Jobs().Info(name, nil)
				if err != nil {
					t.Fatalf("reported job %q is not registered in Nomad: %v", item, err)
				}
				if job.Meta["ultracore.managed_by"] != "ultracore" {
					t.Fatalf("job %q is not marked as ours", item)
				}
			}
			return resources
		},
	})
}
