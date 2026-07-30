package ultra

import (
	"context"
	"encoding/json"
	"time"
)

type FlowID string
type FlowInvocationID string

type Flow struct {
	ID         FlowID
	OrgID      OrgID
	Name       string
	Version    int
	Definition json.RawMessage
	CreatedAt  time.Time
}

type FlowInvocation struct {
	ID          FlowInvocationID
	OrgID       OrgID
	SessionID   SessionID
	FlowID      FlowID
	FlowName    string
	FlowVersion int
	Params      json.RawMessage
	CreatedAt   time.Time
}

type FlowStore interface {
	Put(context.Context, Flow) (Flow, error)
	Get(context.Context, string, int) (Flow, error)
	List(context.Context) ([]Flow, error)
	CreateInvocation(context.Context, FlowInvocation) error
}
