package secrets

import (
	"context"
	"log/slog"
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
// are ignored to avoid degenerate replacements.
func (r *Redactor) Register(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		if len(v) >= 6 {
			r.secrets = append(r.secrets, v)
		}
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
