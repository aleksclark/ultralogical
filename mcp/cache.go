package mcp

import (
	"context"
	"errors"
	"sync"
)

// ErrRevoked is returned by a client whose credentials were invalidated —
// for example because its environment was restarted and its bearer token
// rotated. It is a local failure: a revoked client never reaches the network,
// so a rotated environment can never be reached with stale authority even if
// the remote would still accept the old token.
var ErrRevoked = errors.New("mcp: client revoked")

// Revoke permanently disables a client. Subsequent Initialize, Tools, and
// Call invocations fail with ErrRevoked.
func (c *Client) Revoke() { c.revoked.Store(true) }

// Revoked reports whether the client has been revoked.
func (c *Client) Revoked() bool { return c.revoked.Load() }

// Cache holds one live client per environment, keyed by the environment's
// token epoch. Environment tokens rotate on restart; a cache entry from an
// earlier epoch is revoked and replaced rather than reused, so tool
// discovery and tool calls can never ride a stale token.
type Cache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
	// lastTools remembers the tool names last discovered in an
	// environment. It deliberately outlives the client entry: when an
	// environment becomes unreachable, callers need to know which tools it
	// used to offer so they can fail those calls in a typed way rather than
	// silently shrinking the caller's capabilities.
	lastTools map[string][]string
}

type cacheEntry struct {
	epoch  int
	client *Client
}

// NewCache creates an empty cache.
func NewCache() *Cache {
	return &Cache{entries: map[string]*cacheEntry{}, lastTools: map[string][]string{}}
}

// RememberTools records the tool names discovered in an environment.
func (c *Cache) RememberTools(envID string, names []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastTools == nil {
		c.lastTools = map[string][]string{}
	}
	c.lastTools[envID] = append([]string(nil), names...)
}

// LastTools returns the tool names last discovered in an environment.
func (c *Cache) LastTools(envID string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lastTools[envID]...)
}

// Client returns the cached client for envID at the given epoch, creating one
// when absent. An entry from an older epoch is revoked and replaced; a
// request for an older epoch than the cached one is refused so a late caller
// cannot resurrect rotated authority.
func (c *Cache) Client(envID string, epoch int, endpoint, token string) (*Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*cacheEntry{}
	}
	existing, ok := c.entries[envID]
	switch {
	case ok && existing.epoch == epoch && !existing.client.Revoked():
		return existing.client, nil
	case ok && existing.epoch > epoch:
		return nil, ErrRevoked
	case ok:
		existing.client.Revoke()
	}
	client := NewClient(endpoint, token)
	c.entries[envID] = &cacheEntry{epoch: epoch, client: client}
	return client, nil
}

// Invalidate revokes and drops any cached client for an environment.
func (c *Cache) Invalidate(envID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[envID]; ok {
		entry.client.Revoke()
		delete(c.entries, envID)
	}
}

// Epoch reports the cached epoch for an environment and whether an entry
// exists, for diagnostics and tests.
func (c *Cache) Epoch(envID string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[envID]
	if !ok {
		return 0, false
	}
	return entry.epoch, true
}

// guard fails fast for revoked clients before any network use.
func (c *Client) guard(ctx context.Context) error {
	if c.revoked.Load() {
		return ErrRevoked
	}
	return ctx.Err()
}
