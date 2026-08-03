package core

import (
	"context"
	"encoding/json"
	"time"
)

// EnvID identifies a session-owned development environment.
type EnvID string

// ProviderInstanceID identifies an org-scoped registration of where envs run.
type ProviderInstanceID string

// EnvState is the development-environment state machine.
type EnvState string

const (
	EnvRequested    EnvState = "requested"
	EnvProvisioning EnvState = "provisioning"
	EnvReady        EnvState = "ready"
	EnvSuspended    EnvState = "suspended"
	EnvTerminating  EnvState = "terminating"
	EnvTerminated   EnvState = "terminated"
	EnvFailed       EnvState = "failed"
)

// Terminal reports whether an environment is finished.
func (s EnvState) Terminal() bool { return s == EnvTerminated || s == EnvFailed }

// EnvSpec is provider-neutral desired configuration.
type EnvSpec struct {
	Name     string            `json:"name"`
	Image    string            `json:"image,omitempty"`
	Workdir  string            `json:"workdir,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ProviderHandle is opaque, versioned provider state persisted with the env.
type ProviderHandle struct {
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data"`
}

// ProviderStatus is the provider's actual resource status.
type ProviderStatus struct {
	State   EnvState
	Message string
}

// EnvProvider is the provider seam. Implementations are dependency adapters
// (local Docker, Nomad, Kubernetes, tunnel); all must pass the shared
// conformance suite.
type EnvProvider interface {
	Provision(ctx context.Context, envID EnvID, spec EnvSpec, token string) (ProviderHandle, error)
	Status(ctx context.Context, handle ProviderHandle) (ProviderStatus, error)
	Endpoint(ctx context.Context, handle ProviderHandle) (string, error)
	Restart(ctx context.Context, envID EnvID, handle ProviderHandle, spec EnvSpec, token string) (ProviderHandle, error)
	Terminate(ctx context.Context, handle ProviderHandle) error
}

// EnvAdopter is the optional recovery seam. Provisioning is a two-step
// operation — create the resource, then persist its handle — so a control
// plane that dies between the steps would otherwise create a second resource
// on retry. A provider that can find the resource it already created for an
// environment lets the retry adopt it instead of duplicating it.
type EnvAdopter interface {
	Adopt(ctx context.Context, envID EnvID) (handle ProviderHandle, found bool, err error)
}

// EnvResourceLister is the optional leak-check seam: a provider that can
// enumerate the concrete resources it owns for an environment lets the
// conformance suite prove termination actually released them. Returning an
// empty slice means nothing is left.
type EnvResourceLister interface {
	Resources(ctx context.Context, envID EnvID) ([]string, error)
}

// DevEnv is one session-owned environment.
type DevEnv struct {
	ID                 EnvID
	OrgID              OrgID
	SessionID          SessionID
	ProviderInstanceID ProviderInstanceID
	State              EnvState
	Spec               EnvSpec
	Handle             ProviderHandle
	Endpoint           string
	TokenHash          []byte
	TokenEnc           []byte
	Epoch              int
	FailureMessage string
	CreatedByRunID *RunID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ReadyAt        *time.Time
	TerminatedAt   *time.Time
}

// ProviderInstance is an org-scoped registration of where environments run.
type ProviderInstance struct {
	ID     ProviderInstanceID
	OrgID  OrgID
	Kind   string
	Name   string
	Config json.RawMessage
	State  string
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
)

// EnvStore manages dev envs within one org.
type EnvStore interface {
	Create(ctx context.Context, env DevEnv) error
	Get(ctx context.Context, id EnvID) (DevEnv, error)
	GetForUpdate(ctx context.Context, id EnvID) (DevEnv, error)
	List(ctx context.Context, session SessionID) ([]DevEnv, error)
	ListActive(ctx context.Context) ([]DevEnv, error)
	SetProvisioning(ctx context.Context, id EnvID) error
	// SetHandle records the provider handle before readiness so an
	// interrupted provisioning can adopt its resource instead of creating
	// a duplicate one.
	SetHandle(ctx context.Context, id EnvID, handle ProviderHandle) error
	SetReady(ctx context.Context, id EnvID, handle ProviderHandle, endpoint string) error
	SetFailed(ctx context.Context, id EnvID, message string) error
	// SetSuspended records that an environment's host is temporarily
	// unreachable. It is distinct from failure because the workspace still
	// exists: a user's machine that went offline will come back, and marking
	// it failed would tell every other surface the work was destroyed.
	SetSuspended(ctx context.Context, id EnvID, message string) error
	SetTerminating(ctx context.Context, id EnvID) error
	SetTerminated(ctx context.Context, id EnvID) error
	RotateToken(ctx context.Context, id EnvID, tokenHash, tokenEnc []byte) error
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
