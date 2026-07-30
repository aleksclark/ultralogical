package ultra

import "time"

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
