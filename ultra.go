// Package core defines the core domain types and storage interfaces for
// ultracore. It contains no I/O; implementations live in subpackages
// (postgres, jobqueue/river, ...).
package core

import (
	"time"
)

// OrgID identifies an organization, the tenancy boundary.
type OrgID string

// UserID identifies a human user.
type UserID string

// SessionID identifies a durable work session within an org.
type SessionID string

// OrgRole is a user's role within an org.
type OrgRole string

const (
	OrgRoleOwner  OrgRole = "owner"
	OrgRoleAdmin  OrgRole = "admin"
	OrgRoleMember OrgRole = "member"
)

// Org is the tenancy boundary. Every session, credential, and provider
// instance belongs to exactly one org.
type Org struct {
	ID        OrgID
	Name      string
	CreatedAt time.Time
}

// User is a human identity. Org membership is modeled separately so a user
// can belong to multiple orgs.
type User struct {
	ID        UserID
	Email     string
	Display   string
	CreatedAt time.Time
}

// OrgMember links a user to an org with a role.
type OrgMember struct {
	OrgID    OrgID
	UserID   UserID
	Role     OrgRole
	JoinedAt time.Time
}

// Session is the durable unit of work: it outlives any process, UI, agent
// loop, or environment, and owns an append-only event log.
type Session struct {
	ID         SessionID
	OrgID      OrgID
	Title      string
	CreatedAt  time.Time
	ArchivedAt *time.Time
}
