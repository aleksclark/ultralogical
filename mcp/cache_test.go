package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aleksclark/ultralogical/mcp"
)

// An environment restart rotates its bearer token and bumps its epoch. The
// cache must hand out a fresh client for the new epoch and permanently revoke
// the old one, so nothing can keep calling with the rotated-away token.
func TestCacheEpochInvalidation(t *testing.T) {
	cache := mcp.NewCache()
	ctx := context.Background()

	first, err := cache.Client("env-1", 1, "http://127.0.0.1:1/mcp", "token-one")
	if err != nil {
		t.Fatal(err)
	}
	again, err := cache.Client("env-1", 1, "http://127.0.0.1:1/mcp", "token-one")
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatal("cache returned a new client for the same epoch")
	}
	if epoch, ok := cache.Epoch("env-1"); !ok || epoch != 1 {
		t.Fatalf("cached epoch = %d, %v", epoch, ok)
	}

	rotated, err := cache.Client("env-1", 2, "http://127.0.0.1:2/mcp", "token-two")
	if err != nil {
		t.Fatal(err)
	}
	if rotated == first {
		t.Fatal("cache reused the pre-rotation client after an epoch bump")
	}
	if !first.Revoked() {
		t.Fatal("the pre-rotation client was not revoked")
	}

	// A revoked client fails locally, before any network use, so a stale token
	// can never reach an environment even if the remote would accept it.
	if err := first.Initialize(ctx); !errors.Is(err, mcp.ErrRevoked) {
		t.Fatalf("revoked Initialize error = %v, want ErrRevoked", err)
	}
	if _, err := first.Tools(ctx); !errors.Is(err, mcp.ErrRevoked) {
		t.Fatalf("revoked Tools error = %v, want ErrRevoked", err)
	}
	if _, err := first.Call(ctx, "bash", json.RawMessage(`{"command":"true"}`)); !errors.Is(err, mcp.ErrRevoked) {
		t.Fatalf("revoked Call error = %v, want ErrRevoked", err)
	}

	// A late caller holding an older epoch cannot resurrect rotated authority.
	if _, err := cache.Client("env-1", 1, "http://127.0.0.1:1/mcp", "token-one"); !errors.Is(err, mcp.ErrRevoked) {
		t.Fatalf("stale-epoch request error = %v, want ErrRevoked", err)
	}

	// Explicit invalidation revokes and drops the entry.
	cache.Invalidate("env-1")
	if !rotated.Revoked() {
		t.Fatal("Invalidate did not revoke the cached client")
	}
	if _, ok := cache.Epoch("env-1"); ok {
		t.Fatal("Invalidate left a cache entry behind")
	}
	// After invalidation the environment can be cached again at its epoch.
	if _, err := cache.Client("env-1", 2, "http://127.0.0.1:2/mcp", "token-two"); err != nil {
		t.Fatalf("cache refused a fresh client after invalidation: %v", err)
	}
}

// The last known toolset must outlive the client entry: when an environment
// dies mid-run, the caller still has to name the tools it used to offer so it
// can fail those calls in a typed way instead of silently losing them.
func TestCacheRemembersToolsAcrossInvalidation(t *testing.T) {
	cache := mcp.NewCache()
	if got := cache.LastTools("env-1"); len(got) != 0 {
		t.Fatalf("unknown environment reported tools: %v", got)
	}

	cache.RememberTools("env-1", []string{"bash", "view"})
	if got := cache.LastTools("env-1"); len(got) != 2 || got[0] != "bash" || got[1] != "view" {
		t.Fatalf("remembered tools = %v", got)
	}

	// Losing the client must not lose the knowledge of what it offered.
	cache.Invalidate("env-1")
	if got := cache.LastTools("env-1"); len(got) != 2 {
		t.Fatalf("tools forgotten after invalidation: %v", got)
	}

	// The returned slice is a copy: a caller cannot corrupt the cache.
	got := cache.LastTools("env-1")
	got[0] = "mutated"
	if again := cache.LastTools("env-1"); again[0] != "bash" {
		t.Fatalf("caller mutated cached tool names: %v", again)
	}

	// Environments do not share remembered tools.
	if got := cache.LastTools("env-2"); len(got) != 0 {
		t.Fatalf("tools leaked across environments: %v", got)
	}
}

func TestCacheIsolatesEnvironments(t *testing.T) {
	cache := mcp.NewCache()
	one, err := cache.Client("env-a", 1, "http://127.0.0.1:1/mcp", "a")
	if err != nil {
		t.Fatal(err)
	}
	two, err := cache.Client("env-b", 1, "http://127.0.0.1:2/mcp", "b")
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("two environments share one cached client")
	}
	cache.Invalidate("env-a")
	if two.Revoked() {
		t.Fatal("invalidating one environment revoked another's client")
	}
}
