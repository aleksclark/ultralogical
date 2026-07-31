package secrets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
)

// Redactor scrubs registered secret values from strings. Workers register
// every decrypted credential value; the logging handler and any code that
// surfaces provider errors must pass text through Redact.
type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

// DefaultRedactor is the process-wide redactor used by RedactingHandler.
var DefaultRedactor = &Redactor{}

// Register adds secret values to scrub. Empty and short (<6 chars) values
// are ignored to avoid degenerate replacements. Each value is registered
// alongside the encodings it can acquire on the way to a log line or an error
// message — URL query escaping, JSON string escaping, and base64 — because a
// secret that only survives redaction in its literal form is still leaked.
func (r *Redactor) Register(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		if len(v) < 6 {
			continue
		}
		for _, form := range encodings(v) {
			if len(form) >= 6 && !slices.Contains(r.secrets, form) {
				r.secrets = append(r.secrets, form)
			}
		}
	}
}

// Encodings returns the literal value plus every encoded form the redactor
// scrubs. Leak sweeps use it so a test checks the same forms production
// redacts, instead of guessing.
func Encodings(value string) []string { return encodings(value) }

// encodings returns the literal value plus the encoded forms it can take in
// transports and structured logs.
func encodings(value string) []string {
	quoted, err := json.Marshal(value)
	jsonForm := ""
	if err == nil && len(quoted) >= 2 {
		jsonForm = string(quoted[1 : len(quoted)-1])
	}
	return []string{
		value,
		url.QueryEscape(value),
		url.PathEscape(value),
		base64.StdEncoding.EncodeToString([]byte(value)),
		base64.RawURLEncoding.EncodeToString([]byte(value)),
		jsonForm,
	}
}

// Redact replaces every registered secret in s with "[redacted]".
func (r *Redactor) Redact(s string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, "[redacted]")
	}
	return s
}

// RedactingHandler is a slog.Handler that scrubs registered secrets from
// messages and string attribute values.
type RedactingHandler struct {
	inner    slog.Handler
	redactor *Redactor
}

// NewRedactingHandler wraps a handler with the default redactor.
func NewRedactingHandler(inner slog.Handler) *RedactingHandler {
	return &RedactingHandler{inner: inner, redactor: DefaultRedactor}
}

// NewRedactingHandlerWith wraps a handler with an explicit redactor, so tests
// can assert the production scrubbing behavior without mutating global state.
func NewRedactingHandlerWith(inner slog.Handler, redactor *Redactor) *RedactingHandler {
	return &RedactingHandler{inner: inner, redactor: redactor}
}

// Enabled implements slog.Handler.
func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle implements slog.Handler.
func (h *RedactingHandler) Handle(ctx context.Context, rec slog.Record) error {
	clean := slog.NewRecord(rec.Time, rec.Level, h.redactor.Redact(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(h.redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, clean)
}

func (h *RedactingHandler) redactAttr(a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.redactor.Redact(a.Value.String()))
	case slog.KindAny:
		return slog.String(a.Key, h.redactor.Redact(a.Value.String()))
	default:
		return a
	}
}

// WithAttrs implements slog.Handler.
func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.redactAttr(a)
	}
	return &RedactingHandler{inner: h.inner.WithAttrs(clean), redactor: h.redactor}
}

// WithGroup implements slog.Handler.
func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{inner: h.inner.WithGroup(name), redactor: h.redactor}
}
