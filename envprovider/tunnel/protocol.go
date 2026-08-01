// Package tunnel implements the environment provider for environments running
// on a user's own machine, reached through an outbound tunnel.
//
// The user runs `ultra-env-agent`, which owns a local Docker provider and
// exposes it over a small authenticated control API. The user publishes that
// API through an outbound tunnel, so the platform never needs inbound access
// to the user's network. This package is the platform side: it drives the
// agent's control API and routes tool traffic to the endpoints the agent
// publishes.
//
// Authentication is mutual. The agent authenticates with an org-scoped
// registration token, and the platform signs every control request, so a
// leaked tunnel URL alone is useless.
package tunnel

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	ultra "github.com/aleksclark/ultralogical"
)

// Control API paths.
const (
	PathHealth    = "/control/health"
	PathProvision = "/control/provision"
	PathStatus    = "/control/status"
	PathEndpoint  = "/control/endpoint"
	PathRestart   = "/control/restart"
	PathTerminate = "/control/terminate"
	PathResources = "/control/resources"
	PathRevoke    = "/control/revoke"
)

// Signature headers. The timestamp is signed along with the body so a captured
// request cannot be replayed indefinitely.
const (
	HeaderTimestamp = "X-Ultra-Timestamp"
	HeaderSignature = "X-Ultra-Signature"
)

// SignatureWindow bounds how old a signed request may be.
const SignatureWindow = 5 * time.Minute

// Sign returns the signature for one control request. The path and body are
// both covered: a signature valid for one operation must not authorize
// another.
func Sign(secret, path string, timestamp time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(path))
	mac.Write([]byte{0})
	mac.Write([]byte(strconv.FormatInt(timestamp.Unix(), 10)))
	mac.Write([]byte{0})
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature checks a control request's signature and freshness. It is
// constant-time and rejects a stale timestamp, so a captured request stops
// working rather than being replayable forever.
func VerifySignature(secret, path, timestampHeader, signatureHeader string, body []byte, now time.Time) error {
	if signatureHeader == "" {
		return errors.New("tunnel: request is not signed")
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(timestampHeader), 10, 64)
	if err != nil {
		return errors.New("tunnel: request timestamp is not a unix time")
	}
	sent := time.Unix(seconds, 0)
	if now.Sub(sent) > SignatureWindow || sent.Sub(now) > SignatureWindow {
		return fmt.Errorf("tunnel: request timestamp is outside the %s window", SignatureWindow)
	}
	expected := Sign(secret, path, sent, body)
	if !hmac.Equal([]byte(expected), []byte(signatureHeader)) {
		return errors.New("tunnel: signature does not match")
	}
	return nil
}

// ProvisionRequest asks the agent to create an environment.
type ProvisionRequest struct {
	EnvID ultra.EnvID   `json:"env_id"`
	Spec  ultra.EnvSpec `json:"spec"`
	Token string        `json:"token"`
}

// ProvisionResponse reports the agent's local handle for an environment. The
// platform stores it opaquely: only the agent knows what runs behind it.
type ProvisionResponse struct {
	Handle ultra.ProviderHandle `json:"handle"`
}

// HandleRequest names an existing environment by its agent-side handle.
type HandleRequest struct {
	EnvID  ultra.EnvID          `json:"env_id"`
	Handle ultra.ProviderHandle `json:"handle"`
}

// RestartRequest replaces an environment's runtime with a rotated token.
type RestartRequest struct {
	EnvID  ultra.EnvID          `json:"env_id"`
	Handle ultra.ProviderHandle `json:"handle"`
	Spec   ultra.EnvSpec        `json:"spec"`
	Token  string               `json:"token"`
}

// StatusResponse is the agent's view of one environment.
type StatusResponse struct {
	State   ultra.EnvState `json:"state"`
	Message string         `json:"message,omitempty"`
}

// EndpointResponse is the tool endpoint the agent publishes, already
// rewritten to whatever address the platform can reach.
type EndpointResponse struct {
	Endpoint string `json:"endpoint"`
}

// ResourcesResponse enumerates what the agent still holds for an environment.
type ResourcesResponse struct {
	Resources []string `json:"resources"`
}

// HealthResponse is the agent's liveness and identity.
type HealthResponse struct {
	Status      string    `json:"status"`
	Provider    string    `json:"provider"`
	ConnectedAt time.Time `json:"connected_at"`
	// Revoked reports that the agent's lease was withdrawn. A revoked agent
	// answers health so the platform can distinguish "revoked" from "gone",
	// but refuses everything else.
	Revoked bool `json:"revoked,omitempty"`
}
