package ultra

import (
	"context"
	"encoding/json"
	"time"
)

// Grants is the monotonically-decreasing authority attached to a run.
type Grants struct {
	Tools       []string `json:"tools"`
	EnvAll      bool     `json:"env_all"`
	Envs        []EnvID  `json:"envs,omitempty"`
	MaySpawn    bool     `json:"may_spawn"`
	MaxChildren int      `json:"max_children"`
}

// RootGrants are assigned by the server to human-started runs.
func RootGrants() Grants {
	return Grants{Tools: []string{"*"}, EnvAll: true, MaySpawn: true, MaxChildren: 16}
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}
func envSet(values []EnvID) map[EnvID]bool {
	out := map[EnvID]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}

// AllowsTool checks a canonical capability (not a displayed env alias).
func (g Grants) AllowsTool(name string) bool { s := stringSet(g.Tools); return s["*"] || s[name] }

// AllowsEnv checks environment authority.
func (g Grants) AllowsEnv(id EnvID) bool { return g.EnvAll || envSet(g.Envs)[id] }

// SubsetOf validates the privilege lattice.
func (g Grants) SubsetOf(parent Grants) bool {
	if g.EnvAll && !parent.EnvAll {
		return false
	}
	if !parent.EnvAll {
		for _, id := range g.Envs {
			if !parent.AllowsEnv(id) {
				return false
			}
		}
	}
	for _, tool := range g.Tools {
		if !parent.AllowsTool(tool) {
			return false
		}
	}
	if g.MaySpawn && !parent.MaySpawn {
		return false
	}
	return g.MaxChildren <= parent.MaxChildren
}

// ParticipantKind distinguishes humans and agents.
type ParticipantKind string

const (
	ParticipantHuman ParticipantKind = "human"
	ParticipantAgent ParticipantKind = "agent"
)

// PresenceState is a participant's current presence state.
type PresenceState string

const (
	PresenceActive PresenceState = "active"
	PresenceIdle   PresenceState = "idle"
	PresenceLeft   PresenceState = "left"
)

// Participant is one human or agent in a session.
type Participant struct {
	SessionID     SessionID
	Kind          ParticipantKind
	ParticipantID string
	Display       string
	State         PresenceState
	JoinedAt      time.Time
	LastSeenAt    time.Time
	LeftAt        *time.Time
}

// ParticipantStore manages session presence.
type ParticipantStore interface {
	Join(context.Context, Participant) (bool, error)
	Heartbeat(context.Context, SessionID, ParticipantKind, string) error
	Leave(context.Context, SessionID, ParticipantKind, string) (bool, error)
	List(context.Context, SessionID) ([]Participant, error)
	ReapIdle(context.Context, time.Time, int) ([]Participant, error)
}

// SessionMemoryEntry is durable session-scoped structured memory.
type SessionMemoryEntry struct {
	SessionID SessionID
	Key       string
	Value     json.RawMessage
	UpdatedBy Actor
	UpdatedAt time.Time
}

// SessionMemoryStore manages capped session memory.
type SessionMemoryStore interface {
	Get(context.Context, SessionID, string) (SessionMemoryEntry, error)
	List(context.Context, SessionID) ([]SessionMemoryEntry, error)
	Set(context.Context, SessionMemoryEntry) error
	Delete(context.Context, SessionID, string) error
}

// RunWait is a durable fan-in wait for child runs.
type RunWait struct {
	ID          string
	ParentRunID RunID
	StepIndex   int
	ToolCallID  string
	State       string
	Deadline    time.Time
	Result      json.RawMessage
}
type RunWaitMember struct {
	WaitID  string
	RunID   RunID
	Ordinal int
}
type RunWaitStore interface {
	Create(context.Context, RunWait, []RunWaitMember) error
	ListOpenForChild(context.Context, RunID) ([]RunWait, error)
	Members(context.Context, string) ([]RunWaitMember, error)
	Resolve(context.Context, string, json.RawMessage) (bool, error)
}
