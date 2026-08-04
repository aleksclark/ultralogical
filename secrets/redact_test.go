package secrets_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/aleksclark/ultracore/secrets"
)

const canary = "sk-canary-redaction-unit-0451"

// A secret that survives redaction only in its literal form is still leaked:
// transports URL-escape it, structured logs JSON-escape it, and gateways
// base64 it. All of those forms must be scrubbed.
func TestRedactEncodedForms(t *testing.T) {
	redactor := &secrets.Redactor{}
	redactor.Register(canary)

	forms := map[string]string{
		"literal":        canary,
		"url query":      url.QueryEscape(canary),
		"url path":       url.PathEscape(canary),
		"base64":         base64.StdEncoding.EncodeToString([]byte(canary)),
		"base64 raw url": base64.RawURLEncoding.EncodeToString([]byte(canary)),
	}
	for name, form := range forms {
		text := "provider rejected credential " + form + " for org 42"
		got := redactor.Redact(text)
		if strings.Contains(got, form) {
			t.Fatalf("%s form survived redaction: %q", name, got)
		}
		if !strings.Contains(got, "[redacted]") {
			t.Fatalf("%s form was dropped without a redaction marker: %q", name, got)
		}
	}

	// A JSON-encoded payload containing the secret must also be scrubbed,
	// including when escaping alters the literal bytes.
	payload, err := json.Marshal(map[string]string{"api_key": canary + "\n\"quoted\""})
	if err != nil {
		t.Fatal(err)
	}
	redactor.Register(canary + "\n\"quoted\"")
	if got := redactor.Redact(string(payload)); strings.Contains(got, "canary") {
		t.Fatalf("JSON-escaped secret survived redaction: %q", got)
	}
}

func TestRedactingHandlerScrubsAttributes(t *testing.T) {
	var buf bytes.Buffer
	redactor := &secrets.Redactor{}
	redactor.Register(canary)
	log := slog.New(secrets.NewRedactingHandlerWith(slog.NewJSONHandler(&buf, nil), redactor))

	log.Error("vendor call failed", "authorization", "Bearer "+canary,
		"query", "key="+url.QueryEscape(canary))
	log.With("cached_key", canary).Info("resolved credential")

	out := buf.String()
	if strings.Contains(out, canary) {
		t.Fatalf("log output contains the literal secret: %s", out)
	}
	if strings.Contains(out, url.QueryEscape(canary)) {
		t.Fatalf("log output contains the escaped secret: %s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("log output has no redaction marker: %s", out)
	}
}

func TestShortValuesAreNotRegistered(t *testing.T) {
	redactor := &secrets.Redactor{}
	redactor.Register("abc")
	if got := redactor.Redact("abc def"); got != "abc def" {
		t.Fatalf("short value produced a degenerate replacement: %q", got)
	}
}
