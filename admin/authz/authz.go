// Package authz defines operator roles and the per-command permission matrix
// for the private admin surface.
package authz

import (
	"strings"
)

// Role is an operator role. Deny by default.
type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleSecurity Role = "security"
	RoleAdmin    Role = "admin"
)

// Command names match AdminCommandService RPC method names (and audit.command).
const (
	CmdRetryQueueJob          = "RetryQueueJob"
	CmdCancelQueueJob         = "CancelQueueJob"
	CmdCancelRun              = "CancelRun"
	CmdAnswerAwait            = "AnswerAwait"
	CmdExpireAwait            = "ExpireAwait"
	CmdResourceReconcile      = "ResourceReconcile"
	CmdResourceRestart        = "ResourceRestart"
	CmdResourceSuspend        = "ResourceSuspend"
	CmdResourceTerminate      = "ResourceTerminate"
	CmdResourceAdoptionProbe  = "ResourceAdoptionProbe"
	CmdReprobeProvider        = "ReprobeProvider"
	CmdRevokeAPIKey           = "RevokeAPIKey"
	CmdDisableCredential      = "DisableCredential"
	CmdPausePeriodicPrompt    = "PausePeriodicPrompt"
	CmdResumePeriodicPrompt   = "ResumePeriodicPrompt"
	CmdDisconnectSubscriber   = "DisconnectSubscriber"
	CmdExportIncidentEvidence = "ExportIncidentEvidence"
	CmdRevealSecret           = "RevealSecret"
)

// Operator is an authenticated admin principal.
type Operator struct {
	ID   string
	Name string
	Role Role
}

// ParseRole normalizes a role string.
func ParseRole(s string) (Role, bool) {
	switch Role(strings.ToLower(strings.TrimSpace(s))) {
	case RoleViewer, RoleOperator, RoleSecurity, RoleAdmin:
		return Role(strings.ToLower(strings.TrimSpace(s))), true
	default:
		return "", false
	}
}

// Can reports whether role may execute command. Deny by default.
func Can(role Role, command string) bool {
	switch role {
	case RoleAdmin:
		return true
	case RoleSecurity:
		switch command {
		case CmdRevealSecret, CmdRevokeAPIKey, CmdDisableCredential,
			CmdExportIncidentEvidence, CmdCancelRun, CmdExpireAwait:
			return true
		default:
			// security may also do operator commands
			return operatorCommands[command]
		}
	case RoleOperator:
		return operatorCommands[command]
	case RoleViewer:
		return false
	default:
		return false
	}
}

// operatorCommands are mutations allowed for operator (and security/admin).
var operatorCommands = map[string]bool{
	CmdRetryQueueJob:          true,
	CmdCancelQueueJob:         true,
	CmdCancelRun:              true,
	CmdAnswerAwait:            true,
	CmdExpireAwait:            true,
	CmdResourceReconcile:      true,
	CmdResourceRestart:        true,
	CmdResourceSuspend:        true,
	CmdResourceTerminate:      true,
	CmdResourceAdoptionProbe:  true,
	CmdReprobeProvider:        true,
	CmdRevokeAPIKey:           true,
	CmdDisableCredential:      true,
	CmdPausePeriodicPrompt:    true,
	CmdResumePeriodicPrompt:   true,
	CmdDisconnectSubscriber:   true,
	CmdExportIncidentEvidence: true,
	// RevealSecret is security|admin only.
}

// Permissions lists command names the role may execute (for WhoAmI).
func Permissions(role Role) []string {
	all := []string{
		CmdRetryQueueJob, CmdCancelQueueJob, CmdCancelRun, CmdAnswerAwait, CmdExpireAwait,
		CmdResourceReconcile, CmdResourceRestart, CmdResourceSuspend, CmdResourceTerminate,
		CmdResourceAdoptionProbe, CmdReprobeProvider, CmdRevokeAPIKey, CmdDisableCredential,
		CmdPausePeriodicPrompt, CmdResumePeriodicPrompt, CmdDisconnectSubscriber,
		CmdExportIncidentEvidence, CmdRevealSecret,
	}
	var out []string
	for _, c := range all {
		if Can(role, c) {
			out = append(out, c)
		}
	}
	return out
}
