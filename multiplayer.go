package core

import (
	"context"
	"encoding/json"
	"time"
)

// Grants is the per-run tool allowlist attached to a run. "*" means every
// canonical tool. Children inherit the parent's allowlist verbatim; E3 will
// add a consumer policy hook on top of this interim safety net.
type Grants struct {
	Tools []string `json:"tools"`
}

// DefaultGrants are assigned by the server to human-started runs.
func DefaultGrants() Grants {
	return Grants{Tools: []string{"*"}}
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}

// CanonicalTools is every capability a run can be granted: the native tools
// plus the environment tools Bezalel exposes.
//
// It exists so a run can be offered an explicit denial stub for the tools it
// lacks. Simply omitting them would be worse: the agent framework answers an
// unknown tool call by listing every tool that *does* exist, which is an
// existence oracle. A uniform refusal reveals nothing.
func CanonicalTools() []string {
	return []string{
		// Native session and orchestration tools.
		"ask_user", "post_event",
		"session_memory_get", "session_memory_list", "session_memory_set", "session_memory_delete",
		"spawn_agent", "wait_for_agents", "run_agent_cohort",
		"provision_env", "list_envs", "terminate_env",
		// Environment tools served over MCP.
		"bash", "view", "write", "edit", "multiedit", "delete", "ls", "glob", "grep",
		"job_output", "job_kill", "download", "fetch", "web_fetch",
		"lsp_diagnostics", "lsp_references", "lsp_restart",
	}
}

// AllowsTool checks a canonical capability (not a displayed env alias).
func (g Grants) AllowsTool(name string) bool { s := stringSet(g.Tools); return s["*"] || s[name] }

// SessionMemoryEntry is durable session-scoped structured memory.
type SessionMemoryEntry struct {
	SessionID SessionID
	Key       string
	Value     json.RawMessage
	UpdatedBy Actor
	UpdatedAt time.Time
}

// Session memory limits. Both are enforced inside the per-session advisory
// lock, so concurrent writers cannot race past them.
const (
	MaxMemoryKeys     = 200
	MaxMemoryValue    = 64 << 10
	MaxMemoryKeyBytes = 256
)

// ValidMemoryKey reports whether a key follows the dotted-namespace convention
// (for example "investigation.findings.db").
//
// The convention is enforced rather than merely documented: memory is shared
// across every run in a session, so keys with whitespace, control characters,
// or empty segments make the namespace unreadable and invite collisions between
// agents that meant different things.
func ValidMemoryKey(key string) bool {
	if key == "" || len(key) > MaxMemoryKeyBytes {
		return false
	}
	segment := 0
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c == '.':
			if segment == 0 {
				return false // empty segment: leading, trailing, or ".."
			}
			segment = 0
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
			segment++
		default:
			return false
		}
	}
	return segment > 0
}

// SessionMemoryStore manages capped session memory.
type SessionMemoryStore interface {
	Get(context.Context, SessionID, string) (SessionMemoryEntry, error)
	List(context.Context, SessionID) ([]SessionMemoryEntry, error)
	Set(context.Context, SessionMemoryEntry) error
	Delete(context.Context, SessionID, string) error
}

// Wait states. A wait is created open and leaves that state exactly once:
// resolved when every member reached a terminal state, timed_out when the
// deadline passed first, or abandoned when the parent itself went terminal.
const (
	WaitOpen      = "open"
	WaitResolved  = "resolved"
	WaitTimedOut  = "timed_out"
	WaitAbandoned = "abandoned"
)

// Wait kinds distinguish an explicit wait_for_agents call from the fan-in half
// of a run_agent_cohort. They behave identically but read differently in the
// event log and in clients.
const (
	WaitKindWait   = "wait"
	WaitKindCohort = "cohort"
)

// Timeout policies. resolve returns partial results to the parent when the
// deadline passes; fail turns the parent's tool call into an error instead.
const (
	TimeoutPolicyResolve = "resolve"
	TimeoutPolicyFail    = "fail"
)

// RunWait is a durable fan-in wait for child runs.
//
// Exactly-once resolution and at-most-once parent resumption are properties of
// this row rather than of any process: leaving `open` is a conditional update,
// and ResumedAt records that the parent's next step was enqueued. A partial
// unique index also allows a parent at most one open wait, so which wait
// resumes a parent is never ambiguous.
type RunWait struct {
	ID            string
	ParentRunID   RunID
	StepIndex     int
	ToolCallID    string
	Kind          string
	State         string
	TimeoutPolicy string
	Deadline      time.Time
	Result        json.RawMessage
	ResumedAt     *time.Time
}

// Terminal reports whether a wait has left the open state.
func (w RunWait) Terminal() bool { return w.State != WaitOpen }

type RunWaitMember struct {
	WaitID  string
	RunID   RunID
	Ordinal int
}

// WaitMemberResult is one child's contribution to a wait's aggregate result.
// Ordinal preserves the order the parent declared its children in, so cohort
// results stay positionally meaningful.
type WaitMemberResult struct {
	Ordinal        int             `json:"ordinal"`
	RunID          RunID           `json:"run_id"`
	State          RunState        `json:"state"`
	Result         json.RawMessage `json:"result,omitempty"`
	FailureReason  string          `json:"failure_reason,omitempty"`
	FailureMessage string          `json:"failure_message,omitempty"`
}

// WaitOutcome is the aggregate injected back into the parent's history as the
// result of its original tool call.
type WaitOutcome struct {
	WaitID    string             `json:"wait_id"`
	Kind      string             `json:"kind"`
	State     string             `json:"state"`
	TimedOut  bool               `json:"timed_out,omitempty"`
	Members   []WaitMemberResult `json:"members"`
	Completed int                `json:"completed"`
	Failed    int                `json:"failed"`
	Cancelled int                `json:"cancelled"`
	Pending   int                `json:"pending,omitempty"`
}

type RunWaitStore interface {
	Create(context.Context, RunWait, []RunWaitMember) error
	Get(context.Context, string) (RunWait, error)
	// GetForUpdate locks the wait row for the surrounding transaction, which
	// is how concurrent terminal children and the timeout sweeper serialize.
	GetForUpdate(context.Context, string) (RunWait, error)
	ListOpenForChild(context.Context, RunID) ([]RunWait, error)
	// ListOpenForParent returns a parent's open waits, used when the parent
	// itself goes terminal and its waits must be abandoned.
	ListOpenForParent(context.Context, RunID) ([]RunWait, error)
	// ListForParent returns every wait a parent has held, open or closed, so
	// clients can render why a run parked and how that wait ended.
	ListForParent(context.Context, RunID) ([]RunWait, error)
	// ClaimDue atomically claims open waits whose deadline has passed,
	// returning at most limit of them. Claiming is itself a state transition,
	// so two sweepers cannot both time out the same wait.
	ClaimDue(context.Context, time.Time, int) ([]RunWait, error)
	Members(context.Context, string) ([]RunWaitMember, error)
	// Close leaves the open state with the given terminal state and result.
	// It reports false when the wait was already closed by someone else.
	Close(ctx context.Context, id, state string, result json.RawMessage) (bool, error)
	// SetResult records the aggregate for a wait that is already closed. The
	// timeout sweeper closes a wait by claiming it, before the outcome can be
	// computed, so the result is written immediately afterwards.
	SetResult(ctx context.Context, id string, result json.RawMessage) error
	// MarkResumed records that the parent's next step was enqueued. It
	// reports false when a resumption was already recorded, which is what
	// makes parent resumption at-most-once.
	MarkResumed(context.Context, string) (bool, error)
}
