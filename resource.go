package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ResourceID identifies a session-owned resource.
type ResourceID string

// ProviderInstanceID identifies an org-scoped registration of where resources run.
type ProviderInstanceID string

// ResourceKind is the kind of resource a provider adapter hosts (e.g. "dev_env").
// ProviderInstance.Kind is the adapter kind (local_docker, byo_k8s, …); Resource.Kind
// is the resource kind the adapter produces. Registration's adapter Kind() returns
// the resource kind.
type ResourceKind string

const (
	// ResourceKindDevEnv is the development-environment resource kind.
	ResourceKindDevEnv ResourceKind = "dev_env"
	// ResourceKindNullResource is a test-only lifecycle-only kind with no tool endpoint.
	ResourceKindNullResource ResourceKind = "null_resource"
)

// ResourceState is the resource lifecycle state machine.
type ResourceState string

const (
	ResourceRequested    ResourceState = "requested"
	ResourceProvisioning ResourceState = "provisioning"
	ResourceReady        ResourceState = "ready"
	ResourceSuspended    ResourceState = "suspended"
	ResourceTerminating  ResourceState = "terminating"
	ResourceTerminated   ResourceState = "terminated"
	ResourceFailed       ResourceState = "failed"
)

// Terminal reports whether a resource is finished.
func (s ResourceState) Terminal() bool {
	return s == ResourceTerminated || s == ResourceFailed
}

// ToolEndpoint is the authenticated tool URL a ready resource may publish.
// The zero value means the resource is lifecycle-only (no tool surface).
type ToolEndpoint string

// DevEnvSpec is the kind-specific schema for ResourceKindDevEnv.
// Validated by the provider's ValidateSpec; stored as Resource.Spec raw JSON.
type DevEnvSpec struct {
	Name     string            `json:"name"`
	Image    string            `json:"image,omitempty"`
	Workdir  string            `json:"workdir,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ParseDevEnvSpec decodes a Resource.Spec as DevEnvSpec.
func ParseDevEnvSpec(spec json.RawMessage) (DevEnvSpec, error) {
	if len(spec) == 0 {
		return DevEnvSpec{}, fmt.Errorf("empty dev_env spec")
	}
	var out DevEnvSpec
	if err := json.Unmarshal(spec, &out); err != nil {
		return DevEnvSpec{}, fmt.Errorf("decode dev_env spec: %w", err)
	}
	return out, nil
}

// ResourceStatus is what Status probing reports (used by reconcile).
type ResourceStatus struct {
	State   ResourceState
	Message string
}

// ResourceProvider is the provider seam. Token is passed separately because
// cleartext is never stored on Resource (only TokenHash/TokenEnc).
// Handle is provider-owned opaque JSON. Empty handle (len==0 or version 0)
// means not yet acquired.
//
// Status and Endpoint stay on the seam because reconcile needs suspension
// detection (tunnel) and readiness polling. HealthCheck is a convenience
// used when serves_tool_endpoint; implementers typically wrap Status ready.
type ResourceProvider interface {
	Kind() ResourceKind
	ValidateSpec(spec json.RawMessage) error
	// Provision creates the underlying resource. Returns opaque handle.
	// Endpoint may stay empty until ready; resourcework polls Status+Endpoint.
	Provision(ctx context.Context, r Resource, token string) (handle json.RawMessage, err error)
	Status(ctx context.Context, r Resource) (ResourceStatus, error)
	Endpoint(ctx context.Context, r Resource) (ToolEndpoint, error)
	Restart(ctx context.Context, r Resource, token string) (handle json.RawMessage, err error)
	Terminate(ctx context.Context, r Resource) error
	// HealthCheck is a convenience used when serves_tool_endpoint.
	HealthCheck(ctx context.Context, r Resource) error
}

// ResourceAdopter is the optional recovery seam. Provisioning is a two-step
// operation — create the resource, then persist its handle — so a control
// plane that dies between the steps would otherwise create a second resource
// on retry. A provider that can find the resource it already created lets the
// retry adopt it instead of duplicating it.
type ResourceAdopter interface {
	Adopt(ctx context.Context, r Resource) (handle json.RawMessage, found bool, err error)
}

// OwnedResource is one concrete control-plane object a provider still holds.
type OwnedResource struct {
	ResourceID  ResourceID
	Descriptors []string
}

// ResourceLister enumerates resources the provider still owns (leak checks).
// Prefer filtering by ResourceID when checking one resource.
type ResourceLister interface {
	ListOwned(ctx context.Context) ([]OwnedResource, error)
}

// Resource is one session-owned durable resource.
type Resource struct {
	ID                 ResourceID
	TenantID           TenantID
	SessionID          SessionID
	Kind               ResourceKind
	ProviderInstanceID ProviderInstanceID
	State              ResourceState
	Spec               json.RawMessage // kind-specific
	Handle             json.RawMessage // provider-owned opaque
	Endpoint           ToolEndpoint
	TokenHash          []byte
	TokenEnc           []byte
	Epoch              int
	FailureMessage     string
	CreatedByRunID     *RunID
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ReadyAt            *time.Time
	TerminatedAt       *time.Time
}

// HandlePresent reports whether a resource handle has been acquired.
// Handles are stored as versioned JSON {"version":N,"data":...}; version 0,
// null, empty, or "{}" means not yet acquired. Non-versioned non-empty JSON
// is also treated as present.
func HandlePresent(h json.RawMessage) bool {
	if len(h) == 0 || string(h) == "null" || string(h) == "{}" {
		return false
	}
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(h, &probe); err == nil {
		if probe.Version > 0 {
			return true
		}
		// versioned shape with version 0
		var full map[string]json.RawMessage
		if err := json.Unmarshal(h, &full); err == nil {
			if _, ok := full["version"]; ok {
				return false
			}
		}
	}
	return true
}

// ProviderInstance is a tenant-scoped registration of where resources run.
// Kind is the provider adapter kind (local_docker, byo_k8s, …). The resource
// kind the adapter hosts is returned by ResourceProvider.Kind().
type ProviderInstance struct {
	ID       ProviderInstanceID
	TenantID TenantID
	Kind     string
	Name     string
	Config   json.RawMessage
	State    string
	// Capabilities is what this registration's control plane answered when it
	// was probed. It is stored rather than recomputed so a decision about what
	// a provider can do never depends on the control plane being reachable at
	// the moment the question is asked.
	Capabilities  ProviderCapabilities
	LastHealthyAt *time.Time
	CreatedAt     time.Time
}

const (
	ProviderKindLocalDocker   = "local_docker"
	ProviderKindBYOKubernetes = "byo_k8s"
	ProviderKindBYONomad      = "byo_nomad"
	ProviderKindTunnelLocal   = "tunnel_local"
	// ProviderKindStatic is the worked example from docs/providers.md. It is a
	// real adapter, not a deployment default: a worker offers it only when a
	// Bezalel binary is configured.
	ProviderKindStatic = "static"
	// ProviderKindNull is a test-only adapter hosting ResourceKindNullResource.
	ProviderKindNull = "null"
)

// ResourceStore manages session resources within one org.
type ResourceStore interface {
	Create(ctx context.Context, r Resource) error
	Get(ctx context.Context, id ResourceID) (Resource, error)
	GetForUpdate(ctx context.Context, id ResourceID) (Resource, error)
	// List returns resources for a session. If kinds is non-empty, only those
	// kinds are returned; empty kinds lists every kind for the session.
	List(ctx context.Context, session SessionID, kinds ...ResourceKind) ([]Resource, error)
	ListActive(ctx context.Context) ([]Resource, error)
	SetProvisioning(ctx context.Context, id ResourceID) error
	// SetHandle records the provider handle before readiness so an
	// interrupted provisioning can adopt its resource instead of creating
	// a duplicate one.
	SetHandle(ctx context.Context, id ResourceID, handle json.RawMessage) error
	SetReady(ctx context.Context, id ResourceID, handle json.RawMessage, endpoint ToolEndpoint) error
	SetFailed(ctx context.Context, id ResourceID, message string) error
	// SetSuspended records that a resource's host is temporarily unreachable.
	// It is distinct from failure because the underlying state still exists:
	// a user's machine that went offline will come back, and marking it failed
	// would tell every other surface the work was destroyed.
	SetSuspended(ctx context.Context, id ResourceID, message string) error
	SetTerminating(ctx context.Context, id ResourceID) error
	SetTerminated(ctx context.Context, id ResourceID) error
	RotateToken(ctx context.Context, id ResourceID, tokenHash, tokenEnc []byte) error
}

// ProviderInstanceStore manages provider registrations within one org.
type ProviderInstanceStore interface {
	Create(ctx context.Context, instance ProviderInstance) error
	Get(ctx context.Context, id ProviderInstanceID) (ProviderInstance, error)
	GetByName(ctx context.Context, name string) (ProviderInstance, error)
	List(ctx context.Context) ([]ProviderInstance, error)
	Delete(ctx context.Context, id ProviderInstanceID) error
	MarkHealthy(ctx context.Context, id ProviderInstanceID) error
}
