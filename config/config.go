// Package config loads CORE_* environment configuration and refuses unknown
// CORE_* variables so config drift cannot silently no-op.
package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Known CORE_* variables accepted by cored and/or coreworker.
// DATABASE_URL is intentionally not CORE_*-prefixed (shared with Postgres tooling).
var known = map[string]bool{
	"CORE_ADDR":               true,
	"CORE_MASTER_KEY":         true,
	"CORE_MIGRATE":            true,
	"CORE_DEFAULT_PROVIDER":   true,
	"CORE_DEFAULT_MODEL":      true,
	"CORE_JOB_TIMEOUT":        true,
	"CORE_RESCUE_AFTER":       true,
	"CORE_MAX_WORKERS":        true,
	"CORE_RECONCILE_INTERVAL": true,
	"CORE_PROVISION_TIMEOUT":  true,
	"CORE_BEZALEL_IMAGE":      true,
	"CORE_BEZALEL_BINARY":     true,
	"CORE_K8S_ENDPOINT_MODE":  true,
	"CORE_K8S_ENDPOINT_HOST":  true,
	"CORE_PROVIDER_KINDS":     true,
	"CORE_K8S_NODEPORT_LOW":   true,
	"CORE_K8S_NODEPORT_HIGH":  true,
	"CORE_OTLP_ENDPOINT":      true,
	"CORE_LOG_LEVEL":          true,
	// CLI / client
	"CORE_URL":    true,
	"CORE_TOKEN":  true,
	"CORE_TENANT": true,
	// coreadmin (private operator API)
	"CORE_ADMIN_ADDR":          true,
	"CORE_ADMIN_TOKEN":         true,
	"CORE_ADMIN_DEV_MODE":      true,
	"CORE_ADMIN_CORS_ORIGIN":   true,
	"CORE_ADMIN_CURSOR_SECRET": true,
	"CORE_ADMIN_TOKEN_ROLE":    true,
	"CORE_ADMIN_TOKENS":        true,
	"CORE_ADMIN_REVEAL_ENABLED": true,
	"CORE_ADMIN_CMD_RATE_LIMIT": true,
	"CORE_ADMIN_CMD_CONCURRENCY": true,
	"CORE_ADMIN_ENABLE_TERMINATE":            true,
	"CORE_ADMIN_ENABLE_SUSPEND":              true,
	"CORE_ADMIN_ENABLE_DISCONNECT_SUBSCRIBER": true,
}

// ErrUnknownEnv is returned when one or more unknown CORE_* vars are set.
type ErrUnknownEnv struct {
	Names []string
}

func (e *ErrUnknownEnv) Error() string {
	sort.Strings(e.Names)
	return fmt.Sprintf("unknown CORE_* environment variable(s): %s", strings.Join(e.Names, ", "))
}

// RefuseUnknown scans the process environment and returns ErrUnknownEnv when
// any CORE_* key is not in the known set.
func RefuseUnknown() error {
	var unknown []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, "CORE_") {
			continue
		}
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return &ErrUnknownEnv{Names: unknown}
	}
	return nil
}

// String returns env or default.
func String(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// Bool returns env parsed as bool, or default. Empty is default.
func Bool(name string, def bool) bool {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// Int returns env parsed as int, or default.
func Int(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// Int32 returns env parsed as int32, or 0 when unset/invalid.
func Int32(name string) int32 {
	v := os.Getenv(name)
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return 0
	}
	return int32(n)
}

// Duration returns env parsed as time.Duration, or default.
func Duration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// CSV splits a comma-separated env value.
func CSV(name string) []string {
	v := os.Getenv(name)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// KnownNames returns the sorted list of known CORE_* keys (for docs/tests).
func KnownNames() []string {
	out := make([]string, 0, len(known))
	for k := range known {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
