package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/openai"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/secrets"
)

// CredentialError is a typed, user-actionable credential failure. Its
// message never contains secret material or raw provider errors.
type CredentialError struct {
	Reason  string // uc.FailureCredentialMissing | uc.FailureCredentialInvalid
	Message string
}

func (e *CredentialError) Error() string { return e.Message }

// ResolveModel resolves a run's model config to a fantasy LanguageModel
// using the org's credential store. Decrypted secret values are registered
// with the default redactor before use.
func ResolveModel(ctx context.Context, scope uc.TenantScope, keyring secrets.Keyring, cfg uc.ModelConfig) (fantasy.LanguageModel, error) {
	kind := uc.InferenceCredentialKind(cfg.Provider)
	if kind == "" {
		return nil, &CredentialError{
			Reason:  uc.FailureCredentialMissing,
			Message: fmt.Sprintf("unknown inference provider %q", cfg.Provider),
		}
	}
	name := cfg.Credential
	if name == "" {
		name = "default"
	}
	cred, err := scope.Credentials().Get(ctx, kind, name)
	if errors.Is(err, uc.ErrNotFound) {
		return nil, &CredentialError{
			Reason:  uc.FailureCredentialMissing,
			Message: fmt.Sprintf("no %s credential named %q — add one in org settings", kind, name),
		}
	}
	if err != nil {
		return nil, fmt.Errorf("loop: load credential: %w", err)
	}
	plaintext, err := keyring.Decrypt(cred.EncPayload)
	if err != nil {
		return nil, fmt.Errorf("loop: decrypt credential: %w", err)
	}
	var payload uc.InferencePayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return nil, fmt.Errorf("loop: decode credential payload: %w", err)
	}
	secrets.DefaultRedactor.Register(payload.APIKey)
	for _, value := range payload.ExtraHeaders {
		secrets.DefaultRedactor.Register(value)
	}

	provider, err := buildProvider(cfg.Provider, payload)
	if err != nil {
		return nil, err
	}
	model, err := provider.LanguageModel(ctx, cfg.ModelID)
	if err != nil {
		return nil, fmt.Errorf("loop: language model: %w", err)
	}
	return model, nil
}

func buildProvider(name string, payload uc.InferencePayload) (fantasy.Provider, error) {
	switch name {
	case "openai":
		opts := []openai.Option{openai.WithAPIKey(payload.APIKey)}
		if len(payload.ExtraHeaders) > 0 {
			opts = append(opts, openai.WithHeaders(payload.ExtraHeaders))
		}
		if payload.BaseURL != "" {
			opts = append(opts, openai.WithBaseURL(payload.BaseURL))
		}
		return openai.New(opts...)
	case "anthropic":
		opts := []anthropic.Option{anthropic.WithAPIKey(payload.APIKey)}
		if len(payload.ExtraHeaders) > 0 {
			opts = append(opts, anthropic.WithHeaders(payload.ExtraHeaders))
		}
		if payload.BaseURL != "" {
			opts = append(opts, anthropic.WithBaseURL(payload.BaseURL))
		}
		return anthropic.New(opts...)
	case "bedrock":
		opts := []bedrock.Option{bedrock.WithAPIKey(payload.APIKey)}
		if len(payload.ExtraHeaders) > 0 {
			opts = append(opts, bedrock.WithHeaders(payload.ExtraHeaders))
		}
		if payload.BaseURL != "" {
			opts = append(opts, bedrock.WithBaseURL(payload.BaseURL))
		}
		return bedrock.New(opts...)
	default:
		return nil, fmt.Errorf("loop: unsupported provider %q", name)
	}
}

// ClassifyProviderError maps a stream error to a typed run failure.
// Auth failures are terminal (never retried); everything else is a generic
// provider error with a redacted, non-secret message.
func ClassifyProviderError(err error) (reason, message string) {
	var perr *fantasy.ProviderError
	if errors.As(err, &perr) {
		if perr.StatusCode == 401 || perr.StatusCode == 403 || perr.AuthError {
			return uc.FailureCredentialInvalid,
				"the inference credential was rejected by the provider — rotate it in org settings"
		}
		return uc.FailureProviderError,
			secrets.DefaultRedactor.Redact(fmt.Sprintf("provider error (HTTP %d)", perr.StatusCode))
	}
	return uc.FailureProviderError, "provider error"
}
