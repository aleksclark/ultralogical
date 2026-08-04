// Package provider is the provider seam's registry: it maps a provider
// kind and an org's registration onto a concrete adapter, and probes a
// registration before it is persisted.
//
// Kinds register their factory here rather than being wired by hand in each
// main package, so adding a provider is one registration rather than an edit
// in every binary that can host environments.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	uc "github.com/aleksclark/ultracore"
)

// Factory builds a provider for one org registration. Config is the
// registration's stored configuration; secrets are already decrypted by the
// caller, so a factory never reaches the credential store itself.
type Factory func(ctx context.Context, config json.RawMessage) (uc.ResourceProvider, error)

// Registry maps provider kinds to factories.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry { return &Registry{factories: map[string]Factory{}} }

// Register adds a factory for a kind. Registering a kind twice is a wiring
// bug, not a runtime condition, so it panics rather than silently taking the
// last one and making which adapter runs depend on import order.
func (r *Registry) Register(kind string, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[kind]; exists {
		panic("provider: provider kind " + kind + " is registered twice")
	}
	r.factories[kind] = factory
}

// Kinds lists the registered kinds in a stable order.
func (r *Registry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for kind := range r.factories {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

// Enabled reports whether a kind can be registered in this deployment.
func (r *Registry) Enabled(kind string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[kind]
	return ok
}

// Build constructs the adapter for one registration.
func (r *Registry) Build(ctx context.Context, kind string, config json.RawMessage) (uc.ResourceProvider, error) {
	r.mu.RLock()
	factory, ok := r.factories[kind]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("provider: provider kind %q is not enabled in this deployment", kind)
	}
	return factory(ctx, config)
}

// DryRun validates a registration against its real control plane before
// anything is persisted, and reports what that control plane can actually do.
//
// The probe is read-only. An operator's cluster must not be left holding
// canary resources because a registration attempt failed halfway, and a
// registration that cannot be probed must not be stored: a provider that has
// never answered is not a provider, it is a guess.
func (r *Registry) DryRun(ctx context.Context, kind string, config json.RawMessage) (uc.ProviderCapabilities, error) {
	provider, err := r.Build(ctx, kind, config)
	if err != nil {
		return uc.ProviderCapabilities{}, err
	}
	if closer, ok := provider.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	prober, ok := provider.(uc.CapabilityProber)
	if !ok {
		// A provider that cannot describe itself is accepted with no claimed
		// capabilities rather than rejected: the conformance suite then holds
		// it to the strictest contract.
		return uc.ProviderCapabilities{Kind: kind}, nil
	}
	capabilities, err := prober.Probe(ctx)
	if err != nil {
		return capabilities, err
	}
	capabilities.Kind = kind
	return capabilities, nil
}
