package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/aleksclark/ultracore/config"
)

func TestRequiredRuntimeKeysDocumented(t *testing.T) {
	// Runtime binaries require DATABASE_URL + CORE_MASTER_KEY (cored/worker)
	// and CORE_ADMIN_TOKEN (+ cursor secret) for coreadmin. These must stay
	// in the known CORE_* set so Nomad Variable injection cannot trip the
	// unknown-env fence.
	need := []string{
		"CORE_MASTER_KEY",
		"CORE_ADDR",
		"CORE_MIGRATE",
		"CORE_ADMIN_ADDR",
		"CORE_ADMIN_TOKEN",
		"CORE_ADMIN_TOKEN_ROLE",
		"CORE_ADMIN_CURSOR_SECRET",
		"CORE_OTLP_ENDPOINT",
		"CORE_MAX_WORKERS",
	}
	known := map[string]bool{}
	for _, n := range config.KnownNames() {
		known[n] = true
	}
	for _, n := range need {
		if !known[n] {
			t.Errorf("required runtime key %s missing from known CORE_* set", n)
		}
	}
}

func TestStringBoolIntDefaults(t *testing.T) {
	t.Setenv("CORE_DEFAULT_MODEL", "")
	if got := config.String("CORE_DEFAULT_MODEL", "gpt-4.1-mini"); got != "gpt-4.1-mini" {
		t.Fatalf("String default=%q", got)
	}
	t.Setenv("CORE_MIGRATE", "true")
	if !config.Bool("CORE_MIGRATE", false) {
		t.Fatal("Bool true")
	}
	t.Setenv("CORE_MAX_WORKERS", "8")
	if config.Int("CORE_MAX_WORKERS", 10) != 8 {
		t.Fatalf("Int got %d", config.Int("CORE_MAX_WORKERS", 10))
	}
}

func TestRefuseUnknownDoesNotFlagDatabaseURL(t *testing.T) {
	// DATABASE_URL is intentionally not CORE_*-prefixed.
	t.Setenv("DATABASE_URL", "postgres://example.invalid/core")
	t.Setenv("CORE_ADDR", ":8080")
	// Clear any leftover unknown from other tests in this package process.
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "CORE_") && name == "CORE_NOT_A_REAL_VAR" {
			_ = os.Unsetenv(name)
		}
	}
	if err := config.RefuseUnknown(); err != nil {
		t.Fatalf("DATABASE_URL must not trip RefuseUnknown: %v", err)
	}
}
