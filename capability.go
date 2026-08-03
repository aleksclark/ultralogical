package core

import "context"

// ProviderCapability names one optional provider behavior. A capability
// changes *how* the shared conformance contract verifies a behavior; it can
// never decide *whether* a behavior is verified. The core contract —
// provision, authenticate, health, execute tools, terminate, and leave nothing
// behind — has no capability flags, because a provider that cannot do those
// things is not a provider.
type ProviderCapability string

// Optional provider capabilities.
const (
	// CapabilityRestartPreservesState means Restart keeps the resource's
	// durable state (workspace for dev_env). A provider without it must still
	// restart and still rotate its token; the contract simply does not require
	// the state to survive.
	CapabilityRestartPreservesState ProviderCapability = "restart_preserves_state"
	// CapabilityToleratesDisconnect means losing transport to the resource
	// is a suspension the provider can recover from, rather than a failure.
	CapabilityToleratesDisconnect ProviderCapability = "tolerates_disconnect"
	// CapabilityAdoptsOrphans means the provider can find a resource it
	// already created, so an interrupted provisioning adopts it instead of
	// creating a second one.
	CapabilityAdoptsOrphans ProviderCapability = "adopts_orphans"
	// CapabilityEnumeratesResources means the provider can list the concrete
	// resources it owns, which is what makes a leak check a positive statement
	// rather than an absence of evidence.
	CapabilityEnumeratesResources ProviderCapability = "enumerates_resources"
	// CapabilityServesToolEndpoint means resources expose the authenticated
	// tool endpoint that health readiness and tool calls require.
	CapabilityServesToolEndpoint ProviderCapability = "serves_tool_endpoint"
)

// OptionalProviderCapabilities is every capability a provider may or may not
// have. Enumerating them means a client can render the whole picture, rather
// than only the ones a given registration happens to support.
func OptionalProviderCapabilities() []ProviderCapability {
	return []ProviderCapability{
		CapabilityRestartPreservesState,
		CapabilityToleratesDisconnect,
		CapabilityAdoptsOrphans,
		CapabilityEnumeratesResources,
		CapabilityServesToolEndpoint,
	}
}

// CoreProviderContract is every lifecycle/auth/leak behavior no capability may
// waive. Tool-surface checks (Discovery, Bash, ExactEdit, LSP, …) live in the
// tool-surface contract and apply only to kinds that serve a tool endpoint.
// A manifest that tries to declare a core check optional is itself a failure.
func CoreProviderContract() []string {
	return []string{
		"Provision", "Health", "TokenRejection",
		"RestartRotatesToken", "Terminate", "LeakCheck",
		"ConcurrentProvisionDistinctEndpoints",
	}
}

// ToolSurfaceProviderContract is every tool-surface behavior required of kinds
// that serve an authenticated tool endpoint (dev_env today).
func ToolSurfaceProviderContract() []string {
	return []string{
		"Discovery", "Bash", "ExactEdit", "LSP",
		"BackgroundJobAndTimeout", "PerCallDeadline",
	}
}

// ProviderCapabilities is what one provider registration can actually do. It
// is discovered by probing the control plane at registration, not declared by
// the kind, so a cluster missing a feature is reported as missing rather than
// assumed present because of its label.
type ProviderCapabilities struct {
	// Kind is the provider kind the capabilities were probed for.
	Kind string `json:"kind"`
	// Supported lists the capabilities the probe confirmed.
	Supported []ProviderCapability `json:"supported"`
	// Notes explains, per unsupported capability, why the control plane
	// cannot offer it. Operators need the reason, not just the absence.
	Notes map[ProviderCapability]string `json:"notes,omitempty"`
}

// Has reports whether a capability was confirmed.
func (c ProviderCapabilities) Has(capability ProviderCapability) bool {
	for _, supported := range c.Supported {
		if supported == capability {
			return true
		}
	}
	return false
}

// Reason explains why a capability is unavailable, for diagnostics.
func (c ProviderCapabilities) Reason(capability ProviderCapability) string {
	if c.Has(capability) {
		return ""
	}
	if note, ok := c.Notes[capability]; ok {
		return note
	}
	return "the provider does not support this capability"
}

// CapabilityProber is the optional registration seam: a provider that can ask
// its own control plane what it supports. Registration probes rather than
// assumes, so a control plane missing a feature reports that fact instead of
// having it inferred from its kind.
type CapabilityProber interface {
	// Probe performs read-only checks against the control plane. It returns
	// an error only when the control plane is unreachable or refuses the
	// caller; a reachable control plane that lacks a feature reports the
	// feature as unsupported with a reason.
	Probe(ctx context.Context) (ProviderCapabilities, error)
}
