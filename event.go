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
	EventKindUserMessage = "user_message"
	EventKindAnnotation  = "annotation"
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

// EventBus delivers ordered, gapless per-session event streams: a catch-up
// read from the log followed by live delivery until ctx is cancelled.
// Authorization must be checked by the caller; the read itself is org-scoped
// so a wrong org yields nothing.
type EventBus interface {
	Subscribe(ctx context.Context, org OrgID, session SessionID, fromSeq int64) (<-chan Event, error)
}
