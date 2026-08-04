package authz

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// TokenEntry is one configured operator token.
type TokenEntry struct {
	Token string
	Role  Role
	Name  string
	ID    string
}

// TokenDirectory resolves bearer tokens to operators.
type TokenDirectory struct {
	entries []TokenEntry
	// DevMode accepts any non-empty bearer as admin when no tokens configured.
	DevMode bool
}

// LoadTokens builds a directory from env:
//
//	CORE_ADMIN_TOKENS JSON map: {"tok":{"role":"operator","name":"ops"}}
//	or fallback CORE_ADMIN_TOKEN + CORE_ADMIN_TOKEN_ROLE (default admin).
func LoadTokens() (*TokenDirectory, error) {
	d := &TokenDirectory{}
	if raw := strings.TrimSpace(os.Getenv("CORE_ADMIN_TOKENS")); raw != "" {
		var m map[string]struct {
			Role string `json:"role"`
			Name string `json:"name"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, fmt.Errorf("CORE_ADMIN_TOKENS: %w", err)
		}
		for tok, meta := range m {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			role, ok := ParseRole(meta.Role)
			if !ok {
				return nil, fmt.Errorf("CORE_ADMIN_TOKENS: invalid role %q for token", meta.Role)
			}
			name := meta.Name
			if name == "" {
				name = string(role)
			}
			id := meta.ID
			if id == "" {
				id = name
			}
			d.entries = append(d.entries, TokenEntry{Token: tok, Role: role, Name: name, ID: id})
		}
		if len(d.entries) == 0 {
			return nil, fmt.Errorf("CORE_ADMIN_TOKENS is empty")
		}
		return d, nil
	}
	if tok := strings.TrimSpace(os.Getenv("CORE_ADMIN_TOKEN")); tok != "" {
		roleStr := os.Getenv("CORE_ADMIN_TOKEN_ROLE")
		if roleStr == "" {
			roleStr = string(RoleAdmin)
		}
		role, ok := ParseRole(roleStr)
		if !ok {
			return nil, fmt.Errorf("CORE_ADMIN_TOKEN_ROLE: invalid role %q", roleStr)
		}
		d.entries = append(d.entries, TokenEntry{
			Token: tok,
			Role:  role,
			Name:  "default",
			ID:    "default",
		})
		return d, nil
	}
	return d, nil
}

// Lookup finds an operator by bearer token using constant-time compare.
func (d *TokenDirectory) Lookup(token string) (Operator, bool) {
	if token == "" {
		return Operator{}, false
	}
	for _, e := range d.entries {
		if subtleConstantTimeEq(token, e.Token) {
			return Operator{ID: e.ID, Name: e.Name, Role: e.Role}, true
		}
	}
	if d.DevMode && len(d.entries) == 0 && token != "" {
		return Operator{ID: "dev", Name: "dev", Role: RoleAdmin}, true
	}
	return Operator{}, false
}

// HasTokens reports whether any token is configured.
func (d *TokenDirectory) HasTokens() bool { return len(d.entries) > 0 }

func subtleConstantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// DirectoryFromEntries builds a directory from explicit entries (tests/wiring).
func DirectoryFromEntries(entries []TokenEntry, devMode bool) *TokenDirectory {
	out := make([]TokenEntry, len(entries))
	copy(out, entries)
	return &TokenDirectory{entries: out, DevMode: devMode}
}
