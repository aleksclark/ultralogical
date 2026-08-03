package core

import (
	"context"
	"time"
)

// Credential kinds. Kind is namespaced: inference credentials carry the
// provider after the colon.
const (
	CredentialKindOpenAI    = "inference:openai"
	CredentialKindAnthropic = "inference:anthropic"
	CredentialKindBedrock   = "inference:bedrock"
)

// InferenceCredentialKind maps a ModelConfig provider name to its credential
// kind, or "" if unknown.
func InferenceCredentialKind(provider string) string {
	switch provider {
	case "openai":
		return CredentialKindOpenAI
	case "anthropic":
		return CredentialKindAnthropic
	case "bedrock":
		return CredentialKindBedrock
	default:
		return ""
	}
}

// Credential is an org-scoped secret. Payload is AES-GCM ciphertext at rest;
// only workers decrypt it, at point of use. Cleartext never appears in
// events, logs, or RPC responses.
type Credential struct {
	OrgID      OrgID
	Kind       string
	Name       string
	EncPayload []byte
	CreatedAt  time.Time
	RotatedAt  time.Time
}

// CredentialInfo is the listable, secret-free view of a credential.
type CredentialInfo struct {
	Kind      string
	Name      string
	CreatedAt time.Time
	RotatedAt time.Time
}

// InferencePayload is the decrypted payload shape for inference credentials.
// BaseURL overrides the vendor default (also how tests point at modelscript).
type InferencePayload struct {
	APIKey       string            `json:"api_key"`
	BaseURL      string            `json:"base_url,omitempty"`
	ExtraHeaders map[string]string `json:"extra_headers,omitempty"`
}

// CredentialStore manages credentials within one org. Put upserts (rotate =
// put with the same kind+name).
type CredentialStore interface {
	Put(ctx context.Context, c Credential) error
	List(ctx context.Context) ([]CredentialInfo, error)
	Get(ctx context.Context, kind, name string) (Credential, error)
	Delete(ctx context.Context, kind, name string) error
}
