package core

import (
	"context"
	"time"
)

type PeriodicPromptID string
type PeriodicPrompt struct {
	ID        PeriodicPromptID
	TenantID  TenantID
	SessionID SessionID
	RunID     RunID
	Schedule  time.Duration
	Prompt    string
	Enabled   bool
	NextAt    time.Time
	CreatedAt time.Time
}
type PeriodicPromptStore interface {
	Create(context.Context, PeriodicPrompt) error
	List(context.Context, SessionID) ([]PeriodicPrompt, error)
	GetForUpdate(context.Context, PeriodicPromptID) (PeriodicPrompt, error)
	SetEnabled(context.Context, PeriodicPromptID, bool) error
	SetNext(context.Context, PeriodicPromptID, time.Time) error
}
