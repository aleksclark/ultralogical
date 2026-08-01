package ultra

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
	FailureMessage     string
	CreatedByRunID     *RunID
	// FlowInvocationID and FlowEnvName are the environment's flow provenance:
	// which invocation created it and which declaration in that flow it
	// satisfies. They are written with the row and never change, which is what
	// makes an invocation's cleanup scope exact.
	FlowInvocationID *FlowInvocationID
	FlowEnvName      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ReadyAt          *time.Time
	TerminatedAt     *time.Time
}

// ProviderInstance is an org-scoped registration of where environments run.
type ProviderInstance struct {
	ID        ProviderInstanceID
	OrgID     OrgID
	Kind      string
	Name      string
	Config    json.RawMessage
	RateClass string
	State     string
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
	ProviderKindHostedEKS     = "hosted_eks"
	ProviderKindBYONomad      = "byo_nomad"
	ProviderKindTunnelLocal   = "tunnel_local"
	RateClassBYO              = "byo"
	RateClassHosted           = "hosted"
)

// EnvUsage is one append-only metering interval. Ready opens it; a
// terminal/suspended state closes it. LastMeteredAt is the crash-safe
// watermark.
type EnvUsage struct {
	ID                 string
	OrgID              OrgID
	EnvID              EnvID
	ProviderInstanceID ProviderInstanceID
	StartedAt          time.Time
	LastMeteredAt      time.Time
	EndedAt            *time.Time
	Seconds            int64
	RateClass          string
}

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

// UsageStore manages the environment metering ledger within one org. The
// ledger is append-only: corrections are compensating rows, never edits.
type UsageStore interface {
	Open(ctx context.Context, usage EnvUsage) error
	Tick(ctx context.Context, envID EnvID, at time.Time) error
	Close(ctx context.Context, envID EnvID, at time.Time) error
	// CloseAtWatermark closes any open interval at its persisted heartbeat
	// rather than at wall time. It is the crash-recovery path: a control
	// plane that died between an environment's terminal transition and its
	// interval close must under-count by at most one heartbeat, never
	// over-count for the time it was dead.
	CloseAtWatermark(ctx context.Context, envID EnvID) error
	// ListOpen returns intervals with no end, for reconciliation.
	ListOpen(ctx context.Context) ([]EnvUsage, error)
	List(ctx context.Context, from, to time.Time) ([]EnvUsage, error)
}
