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
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ReadyAt            *time.Time
	TerminatedAt       *time.Time
}

// ProviderInstance is an org-scoped registration of where environments run.
type ProviderInstance struct {
	ID            ProviderInstanceID
	OrgID         OrgID
	Kind          string
	Name          string
	Config        json.RawMessage
	RateClass     string
	State         string
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
	SetReady(ctx context.Context, id EnvID, handle ProviderHandle, endpoint string) error
	SetFailed(ctx context.Context, id EnvID, message string) error
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

// UsageStore manages the environment metering ledger within one org.
type UsageStore interface {
	Open(ctx context.Context, usage EnvUsage) error
	Tick(ctx context.Context, envID EnvID, at time.Time) error
	Close(ctx context.Context, envID EnvID, at time.Time) error
	List(ctx context.Context, from, to time.Time) ([]EnvUsage, error)
}
