package ultra

import (
	"context"
	"encoding/json"
	"time"
)

// FlowID identifies one immutable version of a flow definition.
type FlowID string

// FlowInvocationID identifies one attempt to run a flow into a session. It is
// the provenance key: every run, environment, and event a flow produces
// carries it forever.
type FlowInvocationID string

// Flow is one immutable version of an org's flow definition. A version is
// never rewritten: a change is a new version, so an invocation that pinned an
// earlier one keeps reproducing the same work.
type Flow struct {
	ID         FlowID
	OrgID      OrgID
	Name       string
	Version    int
	Definition json.RawMessage
	CreatedAt  time.Time
}

// FlowInvocationState is the durable invocation state machine:
//
//	pending → provisioning → running → completed
//	                      ↘ cancelling ↘ failed | cancelled
//
// The state lives on the row rather than in a handler, so provisioning,
// readiness, topology, retries, and cleanup all survive process death.
type FlowInvocationState string

// Flow invocation states.
const (
	FlowInvocationPending      FlowInvocationState = "pending"
	FlowInvocationProvisioning FlowInvocationState = "provisioning"
	FlowInvocationRunning      FlowInvocationState = "running"
	FlowInvocationCancelling   FlowInvocationState = "cancelling"
	FlowInvocationCompleted    FlowInvocationState = "completed"
	FlowInvocationFailed       FlowInvocationState = "failed"
	FlowInvocationCancelled    FlowInvocationState = "cancelled"
)

// Terminal reports whether the invocation admits no further transitions.
func (s FlowInvocationState) Terminal() bool {
	return s == FlowInvocationCompleted || s == FlowInvocationFailed || s == FlowInvocationCancelled
}

// Typed invocation terminal reasons. They are documented outcomes, not raw
// error text, so a client can explain an invocation without parsing messages.
const (
	FlowTerminalCompleted         = "completed"
	FlowTerminalEnvironmentFailed = "environment_failed"
	FlowTerminalAgentFailed       = "agent_failed"
	FlowTerminalCancelled         = "cancelled"
	FlowTerminalInvalidDefinition = "invalid_definition"
	FlowTerminalTimedOut          = "timed_out"
	FlowTerminalInternal          = "internal"
)

// Invocation progress stages. Progress is persisted and keyed, so a
// redelivered advance records nothing twice and replay reconstructs the same
// ordered history.
const (
	FlowStageAccepted      = "accepted"
	FlowStageProvisioning  = "provisioning"
	FlowStageEnvRequested  = "environment_requested"
	FlowStageEnvReady      = "environment_ready"
	FlowStageEnvFailed     = "environment_failed"
	FlowStageAgentsStarted = "agents_started"
	FlowStageAgentTerminal = "agent_terminal"
	FlowStageCleanup       = "cleanup"
	FlowStageTerminal      = "terminal"
)

// FlowInvocation is one durable attempt to run a flow version into a session.
// Rendered holds the exact rendering the invocation used, so editing the flow
// afterwards cannot change what this invocation replays as.
type FlowInvocation struct {
	ID          FlowInvocationID
	OrgID       OrgID
	SessionID   SessionID
	FlowID      FlowID
	FlowName    string
	FlowVersion int
	Params      json.RawMessage
	// Rendered is the persisted RenderedFlow: prompts, grants, and env specs
	// exactly as this invocation resolved them.
	Rendered          json.RawMessage
	State             FlowInvocationState
	TerminalReason    string
	Message           string
	CancelRequestedAt *time.Time
	AdvanceAt         *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// FlowInvocationProgress is one append-only step of an invocation's lifecycle.
// Key makes it idempotent: an advance job that is redelivered records the same
// key and is ignored rather than duplicating history.
type FlowInvocationProgress struct {
	InvocationID FlowInvocationID
	Seq          int64
	Stage        string
	Key          string
	Detail       string
	At           time.Time
}

// FlowStore manages flow definitions and invocations within one org.
type FlowStore interface {
	// Put stores a definition. Version 0 assigns the next version; an
	// explicit version that already exists returns ErrAlreadyExists and
	// leaves the stored definition untouched.
	Put(context.Context, Flow) (Flow, error)
	// Get resolves a name to a version; version 0 means latest.
	Get(context.Context, string, int) (Flow, error)
	// GetByID resolves a pinned version by id, which is how an invocation
	// reloads exactly the definition it was created against.
	GetByID(context.Context, FlowID) (Flow, error)
	// List returns the latest version of every flow in the org.
	List(context.Context) ([]Flow, error)
	// ListVersions returns every version of one flow, newest first.
	ListVersions(context.Context, string) ([]Flow, error)

	CreateInvocation(context.Context, FlowInvocation) error
	GetInvocation(context.Context, FlowInvocationID) (FlowInvocation, error)
	// GetInvocationForUpdate locks the invocation row for the surrounding
	// transaction, which is how concurrent advances serialize.
	GetInvocationForUpdate(context.Context, FlowInvocationID) (FlowInvocation, error)
	ListInvocations(context.Context, SessionID) ([]FlowInvocation, error)
	SetInvocationState(ctx context.Context, id FlowInvocationID, state FlowInvocationState, terminalReason, message string) error
	// RequestInvocationCancel stamps cancel_requested_at (idempotent) and
	// moves a non-terminal invocation to cancelling.
	RequestInvocationCancel(context.Context, FlowInvocationID) error
	// ClaimAdvance reserves the next advance tick for one worker and pushes
	// the tick forward. It reports false when another worker already owns it,
	// which is what stops redelivery from multiplying polling chains.
	ClaimAdvance(ctx context.Context, id FlowInvocationID, next time.Time) (bool, error)
	// AppendProgress records one lifecycle step, reporting false when the
	// key was already recorded.
	AppendProgress(context.Context, FlowInvocationProgress) (bool, error)
	Progress(context.Context, FlowInvocationID) ([]FlowInvocationProgress, error)
	// InvocationRuns and InvocationEnvs enumerate exactly the resources this
	// invocation owns, which is what makes cleanup scoped rather than
	// session-wide.
	InvocationRuns(context.Context, FlowInvocationID) ([]AgentRun, error)
	InvocationEnvs(context.Context, FlowInvocationID) ([]DevEnv, error)
}
