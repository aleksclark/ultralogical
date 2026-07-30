package ultra

import (
	"context"
	"encoding/json"
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
	EventKindUserMessage       = "user_message"
	EventKindAnnotation        = "annotation"
	EventKindRunStarted        = "run_started"
	EventKindStepStarted       = "step_started"
	EventKindTextDelta         = "text_delta"
	EventKindReasoningDelta    = "reasoning_delta"
	EventKindToolCallStart     = "tool_call_started"
	EventKindToolResult        = "tool_result"
	EventKindStepFinished      = "step_finished"
	EventKindRunAwaiting       = "run_awaiting"
	EventKindRunCompleted      = "run_completed"
	EventKindRunFailed         = "run_failed"
	EventKindRunCancelled      = "run_cancelled"
	EventKindEnvRequested      = "env_requested"
	EventKindEnvProvisioning   = "env_provisioning"
	EventKindEnvReady          = "env_ready"
	EventKindEnvFailed         = "env_failed"
	EventKindEnvTerminating    = "env_terminating"
	EventKindEnvTerminated     = "env_terminated"
	EventKindExecPreviewRan    = "exec_preview_ran"
	EventKindParticipantJoined = "participant_joined"
	EventKindParticipantLeft   = "participant_left"
	EventKindParticipantIdle   = "participant_idle"
	EventKindRunSpawned        = "run_spawned"
	EventKindMemorySet         = "memory_set"
	EventKindMemoryDeleted     = "memory_deleted"
	EventKindPermissionDenied  = "permission_denied"
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

// EnvEventPayload is shared by environment lifecycle events.
type EnvEventPayload struct {
	EnvID              EnvID              `json:"env_id"`
	Name               string             `json:"name,omitempty"`
	ProviderInstanceID ProviderInstanceID `json:"provider_instance_id,omitempty"`
	Endpoint           string             `json:"endpoint,omitempty"`
	Message            string             `json:"message,omitempty"`
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
type MemoryEventPayload struct {
	Key       string          `json:"key"`
	UpdatedBy Actor           `json:"updated_by"`
	Value     json.RawMessage `json:"value,omitempty"`
}
type PermissionDeniedPayload struct {
	RunID  RunID  `json:"run_id"`
	Tool   string `json:"tool"`
	EnvID  *EnvID `json:"env_id,omitempty"`
	Reason string `json:"reason"`
}

// EventBus delivers ordered, gapless per-session event streams: a catch-up
// read from the log followed by live delivery until ctx is cancelled.
// Authorization must be checked by the caller; the read itself is org-scoped
// so a wrong org yields nothing.
type EventBus interface {
	Subscribe(ctx context.Context, org OrgID, session SessionID, fromSeq int64) (<-chan Event, error)
}
