package ultra

import (
	"context"
	"time"
)

// ActorType classifies who produced an event.
type ActorType string

const (
	ActorUser   ActorType = "user"
	ActorAgent  ActorType = "agent"
	ActorSystem ActorType = "system"
)

// Actor identifies the producer of an event.
type Actor struct {
	Type ActorType
	ID   string
}

// Event kinds. Every observable thing in a session is a typed event with a
// per-session, gapless, monotonic sequence number. Streaming, multiplayer,
// history replay, and test assertions all subscribe to the same log.
const (
	EventKindUserMessage         = "user_message"
	EventKindAnnotation          = "annotation"
	EventKindRunStarted          = "run_started"
	EventKindStepStarted         = "step_started"
	EventKindTextDelta           = "text_delta"
	EventKindReasoningDelta      = "reasoning_delta"
	EventKindToolCallStart       = "tool_call_started"
	EventKindToolResult          = "tool_result"
	EventKindStepFinished        = "step_finished"
	EventKindRunAwaiting         = "run_awaiting"
	EventKindRunCompleted        = "run_completed"
	EventKindRunFailed           = "run_failed"
	EventKindRunCancelled        = "run_cancelled"
	EventKindEnvRequested        = "env_requested"
	EventKindEnvProvisioning     = "env_provisioning"
	EventKindEnvReady            = "env_ready"
	EventKindEnvFailed           = "env_failed"
	EventKindEnvSuspended        = "env_suspended"
	EventKindEnvTerminating      = "env_terminating"
	EventKindEnvTerminated       = "env_terminated"
	EventKindExecPreviewRan      = "exec_preview_ran"
	EventKindParticipantJoined   = "participant_joined"
	EventKindParticipantLeft     = "participant_left"
	EventKindParticipantIdle     = "participant_idle"
	EventKindRunSpawned          = "run_spawned"
	EventKindMemorySet           = "memory_set"
	EventKindMemoryDeleted       = "memory_deleted"
	EventKindPermissionDenied    = "permission_denied"
	EventKindHistoryCompacted    = "history_compacted"
	EventKindModelFallback       = "model_fallback"
	EventKindHookFired           = "hook_fired"
	EventKindPeriodicPromptFired = "periodic_prompt_fired"
	EventKindFlowInvoked         = "flow_invoked"
	EventKindFlowProgressed      = "flow_invocation_progressed"
	EventKindFlowTerminal        = "flow_invocation_terminal"
)

// Event is one entry in a session's append-only event log. Payload is the
// JSON encoding of the typed payload identified by Kind; the transport layer
// (proto oneof) maps to and from it.
type Event struct {
	SessionID SessionID
	Seq       int64
	TS        time.Time
	Actor     Actor
	Kind      string
	Payload   []byte
}

// Typed event payloads. These are the domain-side shapes; the http package
// maps them to and from the proto EventPayload oneof. JSON field names are
// the storage contract — changing them is a breaking change.

// UserMessagePayload is a human message into the session.
type UserMessagePayload struct {
	Text string `json:"text"`
}

// AnnotationPayload is a free-form note attached to the log.
type AnnotationPayload struct {
	Text string `json:"text"`
}

// RunStartedPayload marks a run's creation.
type RunStartedPayload struct {
	RunID  RunID  `json:"run_id"`
	Prompt string `json:"prompt"`
}

// StepStartedPayload marks the beginning of a step execution attempt.
// Attempt > 1 supersedes any partial deltas from earlier attempts of the
// same step index.
type StepStartedPayload struct {
	RunID     RunID `json:"run_id"`
	StepIndex int   `json:"step_index"`
	Attempt   int   `json:"attempt"`
}

// TextDeltaPayload is a batched chunk of streamed assistant text.
// (StepIndex, Attempt, DeltaIndex) makes ordering and supersession
// unambiguous.
type TextDeltaPayload struct {
	RunID      RunID  `json:"run_id"`
	StepIndex  int    `json:"step_index"`
	Attempt    int    `json:"attempt"`
	DeltaIndex int    `json:"delta_index"`
	Text       string `json:"text"`
}

// ReasoningDeltaPayload is a batched chunk of streamed reasoning text.
type ReasoningDeltaPayload struct {
	RunID      RunID  `json:"run_id"`
	StepIndex  int    `json:"step_index"`
	Attempt    int    `json:"attempt"`
	DeltaIndex int    `json:"delta_index"`
	Text       string `json:"text"`
}

// ToolCallStartedPayload records a completed tool invocation request.
type ToolCallStartedPayload struct {
	RunID      RunID  `json:"run_id"`
	StepIndex  int    `json:"step_index"`
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Input      string `json:"input"` // JSON as sent by the model
}

// ToolResultPayload records a tool execution result.
type ToolResultPayload struct {
	RunID      RunID  `json:"run_id"`
	StepIndex  int    `json:"step_index"`
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error"`
}

// StepFinishedPayload closes a step with its usage audit.
type StepFinishedPayload struct {
	RunID        RunID  `json:"run_id"`
	StepIndex    int    `json:"step_index"`
	TokensIn     int64  `json:"tokens_in"`
	TokensOut    int64  `json:"tokens_out"`
	FinishReason string `json:"finish_reason"`
}

// Question is a structured question an agent asks a human via ask_user.
type Question struct {
	Text    string   `json:"text"`
	Choices []string `json:"choices,omitempty"`
}

// RunAwaitingPayload marks a run parked on human input. No worker slot is
// held while awaiting; PromptRun resumes it.
type RunAwaitingPayload struct {
	RunID    RunID    `json:"run_id"`
	Question Question `json:"question"`
}

// RunCompletedPayload is the run's terminal success event.
type RunCompletedPayload struct {
	RunID     RunID  `json:"run_id"`
	FinalText string `json:"final_text"`
}

// RunFailedPayload is the run's terminal failure event. Reason is one of the
// Failure* constants; Message is user-actionable and never contains raw
// provider errors.
type RunFailedPayload struct {
	RunID   RunID  `json:"run_id"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// RunCancelledPayload is the run's terminal cancellation event.
type RunCancelledPayload struct {
	RunID RunID `json:"run_id"`
}

// EnvEventPayload is shared by environment lifecycle events. Epoch is the
// environment's token generation: it increments on restart so subscribers and
// tool caches can distinguish a restarted environment from its predecessor.
type EnvEventPayload struct {
	EnvID              EnvID              `json:"env_id"`
	Name               string             `json:"name,omitempty"`
	ProviderInstanceID ProviderInstanceID `json:"provider_instance_id,omitempty"`
	Endpoint           string             `json:"endpoint,omitempty"`
	Message            string             `json:"message,omitempty"`
	Epoch              int                `json:"epoch,omitempty"`
}

// ExecPreviewRanPayload records a human command and its real output.
type ExecPreviewRanPayload struct {
	EnvID   EnvID  `json:"env_id"`
	Command string `json:"command"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error"`
}

type ParticipantEventPayload struct {
	Kind          ParticipantKind `json:"kind"`
	ParticipantID string          `json:"participant_id"`
	Display       string          `json:"display,omitempty"`
}
type RunSpawnedPayload struct {
	ParentRunID RunID `json:"parent_run_id"`
	ChildRunID  RunID `json:"child_run_id"`
}

// MemoryEventPayload records a session-memory change. Its JSON field names
// mirror the proto message exactly: the transport decodes stored payloads with
// unknown fields discarded, so a shape that merely looks similar would reach
// clients with those fields silently empty.
type MemoryEventPayload struct {
	Key           string `json:"key"`
	UpdatedByType string `json:"updated_by_type"`
	UpdatedByID   string `json:"updated_by_id"`
	// ValueJSON carries the new value when it is small enough to inline; large
	// values are fetched through the API instead of duplicated into the log.
	ValueJSON string `json:"value_json,omitempty"`
}

// NewMemoryEventPayload builds the payload from a domain actor and value.
func NewMemoryEventPayload(key string, updatedBy Actor, value []byte) MemoryEventPayload {
	return MemoryEventPayload{
		Key:           key,
		UpdatedByType: string(updatedBy.Type),
		UpdatedByID:   updatedBy.ID,
		ValueJSON:     string(value),
	}
}

type HistoryCompactedPayload struct {
	RunID           RunID `json:"run_id"`
	CoveredMessages int   `json:"covered_messages"`
	SummaryTokens   int64 `json:"summary_tokens"`
}
type ModelFallbackPayload struct {
	RunID  RunID  `json:"run_id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}
type HookFiredPayload struct {
	Hook  string `json:"hook"`
	RunID RunID  `json:"run_id,omitempty"`
}
type PeriodicPromptFiredPayload struct {
	RunID  RunID  `json:"run_id"`
	Prompt string `json:"prompt"`
}

type PermissionDeniedPayload struct {
	RunID  RunID  `json:"run_id"`
	Tool   string `json:"tool"`
	EnvID  *EnvID `json:"env_id,omitempty"`
	Reason string `json:"reason"`
}

// FlowInvokedPayload records a flow invocation's full provenance triple, so a
// subscriber can attribute every later run and environment in the session
// without a second request.
type FlowInvokedPayload struct {
	InvocationID FlowInvocationID `json:"invocation_id"`
	FlowID       FlowID           `json:"flow_id"`
	FlowName     string           `json:"flow_name"`
	FlowVersion  int              `json:"flow_version"`
	ParamsJSON   string           `json:"params_json,omitempty"`
}

// FlowProgressedPayload is one ordered lifecycle step of an invocation. Key is
// the same idempotency key the persisted progress row uses, so the log and the
// row set say exactly the same thing.
type FlowProgressedPayload struct {
	InvocationID FlowInvocationID `json:"invocation_id"`
	Stage        string           `json:"stage"`
	Key          string           `json:"key"`
	Detail       string           `json:"detail,omitempty"`
}

// FlowTerminalPayload closes an invocation with its documented reason.
type FlowTerminalPayload struct {
	InvocationID   FlowInvocationID `json:"invocation_id"`
	State          string           `json:"state"`
	TerminalReason string           `json:"terminal_reason"`
	Message        string           `json:"message,omitempty"`
}

// EventBus delivers ordered, gapless per-session event streams: a catch-up
// read from the log followed by live delivery until ctx is cancelled.
// Authorization must be checked by the caller; the read itself is org-scoped
// so a wrong org yields nothing.
type EventBus interface {
	Subscribe(ctx context.Context, org OrgID, session SessionID, fromSeq int64) (<-chan Event, error)
}
