// Package nullresource is a test-only lifecycle-only ResourceProvider. It
// hosts ResourceKindNullResource with no tool endpoint, so the control plane
// can prove kind-agnostic lifecycle without Bezalel.
package nullresource

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/provider/handlefmt"
)

// Provider is an in-memory ResourceProvider.
type Provider struct {
	mu    sync.Mutex
	items map[uc.ResourceID]json.RawMessage
}

// New builds a null provider.
func New() *Provider {
	return &Provider{items: map[uc.ResourceID]json.RawMessage{}}
}

type handleData struct {
	ID string `json:"id"`
}

// Kind implements uc.ResourceProvider.
func (p *Provider) Kind() uc.ResourceKind { return uc.ResourceKindNullResource }

// ValidateSpec accepts {} or {"name":"..."}.
func (p *Provider) ValidateSpec(spec json.RawMessage) error {
	if len(spec) == 0 {
		return nil
	}
	var probe map[string]any
	if err := json.Unmarshal(spec, &probe); err != nil {
		return fmt.Errorf("nullresource: invalid spec: %w", err)
	}
	return nil
}

// Provision stores the resource and returns a versioned handle. No endpoint.
func (p *Provider) Provision(_ context.Context, r uc.Resource, _ string) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, err := handlefmt.EncodeHandle(1, handleData{ID: string(r.ID)})
	if err != nil {
		return nil, err
	}
	p.items[r.ID] = h
	return h, nil
}

// Status reports ready when the resource is still held.
func (p *Provider) Status(_ context.Context, r uc.Resource) (uc.ResourceStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.items[r.ID]; !ok {
		// Fall back to handle existence for adopt/restart paths.
		if !uc.HandlePresent(r.Handle) {
			return uc.ResourceStatus{State: uc.ResourceFailed, Message: "not found"}, nil
		}
	}
	return uc.ResourceStatus{State: uc.ResourceReady}, nil
}

// Endpoint is always empty: this kind serves no tools.
func (p *Provider) Endpoint(context.Context, uc.Resource) (uc.ToolEndpoint, error) {
	return "", nil
}

// Restart keeps the resource and returns a fresh handle envelope.
func (p *Provider) Restart(_ context.Context, r uc.Resource, _ string) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, err := handlefmt.EncodeHandle(1, handleData{ID: string(r.ID)})
	if err != nil {
		return nil, err
	}
	p.items[r.ID] = h
	return h, nil
}

// Terminate drops the resource. Idempotent.
func (p *Provider) Terminate(_ context.Context, r uc.Resource) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.items, r.ID)
	return nil
}

// HealthCheck requires ready status.
func (p *Provider) HealthCheck(ctx context.Context, r uc.Resource) error {
	st, err := p.Status(ctx, r)
	if err != nil {
		return err
	}
	if st.State != uc.ResourceReady {
		return fmt.Errorf("nullresource: not ready")
	}
	return nil
}

// Adopt finds an in-memory resource by id.
func (p *Provider) Adopt(_ context.Context, r uc.Resource) (json.RawMessage, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, ok := p.items[r.ID]
	return h, ok, nil
}

// ListOwned enumerates held resources.
func (p *Provider) ListOwned(context.Context) ([]uc.OwnedResource, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]uc.OwnedResource, 0, len(p.items))
	for id := range p.items {
		out = append(out, uc.OwnedResource{ResourceID: id, Descriptors: []string{"null:" + string(id)}})
	}
	return out, nil
}

// Probe reports lifecycle capabilities only.
func (p *Provider) Probe(context.Context) (uc.ProviderCapabilities, error) {
	return uc.ProviderCapabilities{
		Kind: uc.ProviderKindNull,
		Supported: []uc.ProviderCapability{
			uc.CapabilityAdoptsOrphans,
			uc.CapabilityEnumeratesResources,
		},
		Notes: map[uc.ProviderCapability]string{
			uc.CapabilityServesToolEndpoint:    "null_resource has no tool surface",
			uc.CapabilityRestartPreservesState: "null_resource holds no durable state",
			uc.CapabilityToleratesDisconnect:   "in-process provider has no disconnect path",
		},
	}, nil
}
