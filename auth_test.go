package core_test

import (
	"strings"
	"testing"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/secrets"
)

func TestGenerateAPIKeyFormatAndHash(t *testing.T) {
	raw, prefix, err := uc.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "uck_") || len(raw) < 20 {
		t.Fatalf("raw key shape: %q", raw)
	}
	if prefix != raw[:12] {
		t.Fatalf("prefix = %q, want %q", prefix, raw[:12])
	}
	h1 := uc.HashAPIKey(raw)
	h2 := uc.HashAPIKey(raw)
	if len(h1) != 32 || string(h1) != string(h2) {
		t.Fatalf("hash unstable or wrong length: %d", len(h1))
	}
	if string(h1) == string(uc.HashAPIKey(raw+"x")) {
		t.Fatal("hash collision on different input")
	}
}

func TestAPIKeyRedactorRegistration(t *testing.T) {
	// Mirrors mintKey: raw is registered with the process redactor so logs
	// never echo a live key.
	raw, _, err := uc.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	r := &secrets.Redactor{}
	r.Register(raw)
	got := r.Redact("Authorization: Bearer " + raw)
	if strings.Contains(got, raw) {
		t.Fatalf("raw key survived redact: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("expected redaction marker, got %q", got)
	}
}

func TestParseActorHeader(t *testing.T) {
	a := uc.ParseActorHeader("student/jacob/SmFjb2I")
	if a.Kind != "student" || a.ID != "jacob" || a.Display != "Jacob" {
		t.Fatalf("got %+v", a)
	}
	if uc.EncodeActorHeader(a) != "student/jacob/SmFjb2I" {
		t.Fatalf("round-trip %q", uc.EncodeActorHeader(a))
	}
}
