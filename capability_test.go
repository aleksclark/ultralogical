package core_test

import (
	"strings"
	"testing"

	uc "github.com/aleksclark/ultracore"
)

// A10.1 — a capability manifest can describe how a provider behaves, but it
// can never name a core contract behavior. If it could, "capability-flagged"
// would become a synonym for "untested", which is exactly the weakening the
// phase forbids.
func TestCapabilityManifestCannotNameCoreContract(t *testing.T) {
	core := map[string]bool{}
	for _, name := range uc.CoreProviderContract() {
		core[strings.ToLower(name)] = true
	}
	if len(core) == 0 {
		t.Fatal("the core provider contract is empty; every behavior would be waivable")
	}
	for _, capability := range uc.OptionalProviderCapabilities() {
		if core[strings.ToLower(string(capability))] {
			t.Fatalf("capability %q names a core contract behavior", capability)
		}
	}
	for _, required := range []string{
		"Provision", "Health", "Discovery", "Bash", "TokenRejection", "Terminate", "LeakCheck",
	} {
		if !core[strings.ToLower(required)] {
			t.Fatalf("%s is not in the core contract; a provider could skip it", required)
		}
	}
}

// A10.1 — an unsupported capability reports why, so an operator can act on it
// rather than guess. A capability that is merely absent, with no explanation,
// is a diagnostic dead end.
func TestProviderCapabilitiesExplainWhatIsMissing(t *testing.T) {
	capabilities := uc.ProviderCapabilities{
		Kind:      uc.ProviderKindBYOKubernetes,
		Supported: []uc.ProviderCapability{uc.CapabilityServesToolEndpoint},
		Notes: map[uc.ProviderCapability]string{
			uc.CapabilityAdoptsOrphans: "the cluster cannot list pods by label",
		},
	}
	if !capabilities.Has(uc.CapabilityServesToolEndpoint) {
		t.Fatal("a supported capability reported as missing")
	}
	if capabilities.Reason(uc.CapabilityServesToolEndpoint) != "" {
		t.Fatal("a supported capability reported a reason for being missing")
	}
	if got := capabilities.Reason(uc.CapabilityAdoptsOrphans); got != "the cluster cannot list pods by label" {
		t.Fatalf("probe reason = %q", got)
	}
	if got := capabilities.Reason(uc.CapabilityRestartPreservesWorkspace); got == "" {
		t.Fatal("an unsupported capability with no note explained nothing")
	}
}
