package ultra

import (
	"context"
	"encoding/json"
	"time"
)

// RunID identifies an agent run.
type RunID string

// RunState is the agent-run state machine:
// pending → running ⇄ awaiting → completed | failed | cancelled.
type RunState string

// Run states.
const (
	RunPending   RunState = "pending"
	RunRunning   RunState = "running"
	RunAwaiting  RunState = "awaiting"
	RunCompleted RunState = "completed"
	RunFailed    RunState = "failed"
	RunCancelled RunState = "cancelled"
)

// Terminal reports whether the state admits no further transitions.
func (s RunState) Terminal() bool {
	return s == RunCompleted || s == RunFailed || s == RunCancelled
}

// Typed run-failure reasons. User-actionable; never contain raw provider
// errors (which could leak credentials).
const (
	FailureCredentialMissing = "credential_missing"
	FailureCredentialInvalid = "credential_invalid"
	FailureProviderError     = "provider_error"
	FailureInternal          = "internal"
)

// ModelConfig names the model a run uses and which org credential pays for
// it. Inference is always on org credentials; there is no platform fallback.
type ModelConfig struct {
	Provider   string        `json:"provider"` // openai | anthropic | bedrock
	ModelID    string        `json:"model_id"`
	Credential string        `json:"credential"` // credential name within the org
	Fallbacks  []ModelConfig `json:"fallbacks,omitempty"`
}

// AgentRun is one durable agent loop bound to a session. Its message history
// is persisted per step, so execution-time state is disposable: any worker
// can resume it from the envelope.
type AgentRun struct {
	ID          RunID
	SessionID   SessionID
	OrgID       OrgID
	ParentRunID *RunID
	// SpawnKey identifies the tool call that created this run, formatted as
	// "<parent run>:<step index>:<tool call id>". It is unique per org, so a
	// redelivered step replaying the same spawn adopts the existing child
	// rather than creating a second one.
	SpawnKey string
	// CohortID and CohortOrdinal link children launched by one
	// run_agent_cohort call and preserve their declaration order.
	CohortID          string
	CohortOrdinal     int
	FlowInvocationID  *FlowInvocationID
	Grants            Grants
	Result            json.RawMessage
	State             RunState
	LoopKind          string
	LoopVersion       int
	ModelConfig       ModelConfig
	Prompt            string
	History           json.RawMessage // versioned envelope {"v":1,"messages":[...]}
	FailureReason     string
	FailureMessage    string
	CancelRequestedAt *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// RunStep is the audit row for one executed step.
// UNIQUE(agent_run_id, step_index) makes queue redelivery idempotent.
type RunStep struct {
	RunID        RunID
	StepIndex    int
	Attempt      int
	TokensIn     int64
	TokensOut    int64
	FinishReason string
	CreatedAt    time.Time
}

// RunStore manages agent runs within one org.
type RunStore interface {
	Create(ctx context.Context, r AgentRun) error
	Get(ctx context.Context, id RunID) (AgentRun, error)
	List(ctx context.Context, session SessionID) ([]AgentRun, error)
	// GetForUpdate locks the run row for the duration of the surrounding
	// transaction (SELECT ... FOR UPDATE). Callers must be inside Store.Tx.
	GetForUpdate(ctx context.Context, id RunID) (AgentRun, error)
	// SetHistory replaces the run's history envelope.
	SetHistory(ctx context.Context, id RunID, history json.RawMessage) error
	// SetState transitions the run state; failure fields only apply to
	// RunFailed.
	SetState(ctx context.Context, id RunID, state RunState, failureReason, failureMessage string) error
	// SetResult persists a run's final result so a parent can read what its
	// child produced after the child's process is long gone.
	SetResult(ctx context.Context, id RunID, result json.RawMessage) error
	// GetBySpawnKey returns the run a given spawn tool call already created,
	// or ErrNotFound. This is the read half of spawn idempotency.
	GetBySpawnKey(ctx context.Context, key string) (AgentRun, error)
	// Children lists a run's direct children in creation order, which is what
	// clients render as a run tree.
	Children(ctx context.Context, id RunID) ([]AgentRun, error)
	// RequestCancel stamps cancel_requested_at (idempotent).
	RequestCancel(ctx context.Context, id RunID) error
	// InsertStep records a step audit row; returns ErrAlreadyExists on a
	// duplicate (run, step_index) — the redelivery guard.
	InsertStep(ctx context.Context, s RunStep) error
	// CountStepAttempts returns how many StepStarted attempts have been
	// recorded for a step index (used to number attempt markers).
	Steps(ctx context.Context, id RunID) ([]RunStep, error)
	CountChildren(ctx context.Context, id RunID) (int, error)
}
