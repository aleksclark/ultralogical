package query

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// CursorPayload is the signed keyset cursor body.
type CursorPayload struct {
	// V is the cursor format version.
	V int `json:"v"`
	// Collection name this cursor belongs to.
	Collection string `json:"c"`
	// Fingerprint of the query (filters+sorts+text) so cursors cannot be
	// replayed against a different predicate set.
	Fingerprint string `json:"fp"`
	// SortValues holds the ordered sort key values of the last returned row,
	// including the primary-key tie-breaker.
	SortValues []string `json:"sv"`
	// Exp is unix expiry.
	Exp int64 `json:"exp"`
}

// Signer creates and verifies opaque cursors.
type Signer struct {
	Secret []byte
	TTL    time.Duration
	Now    func() time.Time
}

func (s *Signer) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return DefaultCursorTTL
}

func (s *Signer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// Sign encodes and signs a cursor payload.
func (s *Signer) Sign(p CursorPayload) (string, error) {
	if s == nil || len(s.Secret) == 0 {
		return "", fmt.Errorf("query: cursor signer secret required")
	}
	p.V = 1
	if p.Exp == 0 {
		p.Exp = s.now().Add(s.ttl()).Unix()
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write(raw)
	sig := mac.Sum(nil)
	token := append(sig, raw...)
	return base64.RawURLEncoding.EncodeToString(token), nil
}

// Verify decodes and checks a cursor token.
func (s *Signer) Verify(token string) (CursorPayload, error) {
	if s == nil || len(s.Secret) == 0 {
		return CursorPayload{}, fmt.Errorf("query: cursor signer secret required")
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) <= sha256.Size {
		return CursorPayload{}, invalid(ErrBadCursor, "malformed cursor")
	}
	sig, body := raw[:sha256.Size], raw[sha256.Size:]
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return CursorPayload{}, invalid(ErrBadCursor, "cursor signature invalid")
	}
	var p CursorPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return CursorPayload{}, invalid(ErrBadCursor, "cursor payload invalid")
	}
	if p.V != 1 {
		return CursorPayload{}, invalid(ErrBadCursor, "unsupported cursor version")
	}
	if p.Exp > 0 && s.now().Unix() > p.Exp {
		return CursorPayload{}, invalid(ErrBadCursor, "cursor expired")
	}
	return p, nil
}
