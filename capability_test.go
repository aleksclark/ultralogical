package ultra_test

import (
	"strings"
	"testing"

	ultra "github.com/aleksclark/ultralogical"
)

// A10.1 — a capability manifest can describe how a provider behaves, but it
// can never name a core contract behavior. If it could, "capability-flagged"
// would become a synonym for "untested", which is exactly the weakening the
// phase forbids.
func TestCapabilityManifestCannotNameCoreContract(t *testing.T) {
	core := map[string]bool{}
	for _, name := range ultra.CoreProviderContract() {
		core[strings.ToLower(name)] = true
	}
	if len(core) == 0 {
		t.Fatal("the core provider contract is empty; every behavior would be waivable")
	}
	// Every optional capability must be genuinely optional: none of them may
	// collide with a core contract step.
	optional := []ultra.ProviderCapability{
		ultra.CapabilityRestartPreservesWorkspace,
		ultra.CapabilityToleratesDisconnect,
		ultra.CapabilityAdoptsOrphans,
		ultra.CapabilityEnumeratesResources,
		ultra.CapabilityServesToolEndpoint,
		ultra.CapabilityNamespaceIsolation,
		ultra.CapabilityResourceQuota,
	}
	for _, capability := range optional {
		if core[strings.ToLower(string(capability))] {
			t.Fatalf("capability %q names a core contract behavior", capability)
		}
	}
	// The behaviors a run cannot survive without must all be core.
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
	capabilities := ultra.ProviderCapabilities{
		Kind:      ultra.ProviderKindBYOKubernetes,
		Supported: []ultra.ProviderCapability{ultra.CapabilityServesToolEndpoint},
		Notes: map[ultra.ProviderCapability]string{
			ultra.CapabilityResourceQuota: "the cluster exposes no ResourceQuota API",
		},
	}
	if !capabilities.Has(ultra.CapabilityServesToolEndpoint) {
		t.Fatal("a supported capability reported as missing")
	}
	if capabilities.Reason(ultra.CapabilityServesToolEndpoint) != "" {
		t.Fatal("a supported capability reported a reason for being missing")
	}
	if got := capabilities.Reason(ultra.CapabilityResourceQuota); got != "the cluster exposes no ResourceQuota API" {
		t.Fatalf("probe reason = %q", got)
	}
	// An unsupported capability with no recorded note still explains itself
	// rather than returning an empty string a caller would render as blank.
	if got := capabilities.Reason(ultra.CapabilityNamespaceIsolation); got == "" {
		t.Fatal("an unsupported capability with no note explained nothing")
	}
}
