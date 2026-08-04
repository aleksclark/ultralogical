// Package core defines the core domain types and storage interfaces for
// ultracore. It contains no I/O; implementations live in subpackages
// (postgres, jobqueue/river, ...).
package core

import (
	"fmt"
	"time"
	"unicode/utf8"
)

// TenantID identifies a tenant, the tenancy boundary.
type TenantID string

// SessionID identifies a durable work session within a tenant.
type SessionID string

// Tenant is the tenancy boundary. Every session, credential, provider
// instance, and API key belongs to exactly one tenant. One consumer service
// typically maps to one tenant (or several for staging/prod); the core does
// not interpret the name.
type Tenant struct {
	ID        TenantID
	Name      string
	CreatedAt time.Time
}

// Session label limits. Enforced on write so list selectors stay cheap.
const (
	MaxSessionLabels      = 16
	MaxSessionLabelKeyLen = 128
	MaxSessionLabelValLen = 128
)

// Session is the durable unit of work: it outlives any process, UI, agent
// loop, or resource, and owns an append-only event log. Labels are a
// consumer-defined taxonomy the core indexes but never interprets.
type Session struct {
	ID         SessionID
	TenantID   TenantID
	Title      string
	Labels     map[string]string
	CreatedAt  time.Time
	ArchivedAt *time.Time
}

// ValidateLabels checks label cardinality and key/value length limits.
func ValidateLabels(labels map[string]string) error {
	if len(labels) > MaxSessionLabels {
		return fmt.Errorf("labels: at most %d pairs", MaxSessionLabels)
	}
	for k, v := range labels {
		if k == "" {
			return fmt.Errorf("labels: empty key")
		}
		if utf8.RuneCountInString(k) > MaxSessionLabelKeyLen {
			return fmt.Errorf("labels: key %q exceeds %d characters", k, MaxSessionLabelKeyLen)
		}
		if utf8.RuneCountInString(v) > MaxSessionLabelValLen {
			return fmt.Errorf("labels: value for %q exceeds %d characters", k, MaxSessionLabelValLen)
		}
	}
	return nil
}
