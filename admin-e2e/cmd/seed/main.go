// seed inserts admin e2e fixtures: many tenants, one canary API key, one
// credential, and a few sessions/events/runs so list smokes are non-empty.
// Invoked by scripts/admin-e2e-stack.sh after coreadmin has migrated.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "admin-e2e seed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	canary := os.Getenv("ADMIN_E2E_CANARY_KEY")
	if canary == "" {
		canary = "sk-canary-XyZZy-0451-leak-detector"
	}
	n := 60
	if v := os.Getenv("ADMIN_E2E_TENANT_COUNT"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 {
			return fmt.Errorf("ADMIN_E2E_TENANT_COUNT: %q", v)
		}
		n = parsed
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	var firstTenant string
	for i := 0; i < n; i++ {
		id := uuid.NewString()
		name := fmt.Sprintf("admin-e2e-tenant-%03d", i)
		if _, err := pool.Exec(ctx,
			`INSERT INTO tenants (id, name) VALUES ($1, $2)`, id, name); err != nil {
			return fmt.Errorf("tenant %d: %w", i, err)
		}
		if i == 0 {
			firstTenant = id
		}
	}

	// Canary API key metadata only — store hash/ciphertext, never expose raw.
	keyID := uuid.NewString()
	prefix := "uck_canary"
	if len(canary) >= 12 {
		prefix = canary[:12]
	}
	sum := sha256.Sum256([]byte(canary))
	keyHash := sum[:]
	if _, err := pool.Exec(ctx, `
		INSERT INTO api_keys (id, tenant_id, name, scope, prefix, key_hash, key_enc)
		VALUES ($1, $2, 'canary', 'admin', $3, $4, $5)`,
		keyID, firstTenant, prefix, keyHash, []byte("ciphertext-not-plaintext"),
	); err != nil {
		return fmt.Errorf("api key: %w", err)
	}

	// Credential ciphertext metadata — plaintext must never appear in admin responses.
	encPayload := []byte("enc:" + canary)
	if _, err := pool.Exec(ctx, `
		INSERT INTO credentials (tenant_id, kind, name, enc_payload)
		VALUES ($1, 'openai', 'default', $2)`,
		firstTenant, encPayload,
	); err != nil {
		return fmt.Errorf("credential: %w", err)
	}

	// A few sessions, events, and runs for list smokes.
	for i := 0; i < 5; i++ {
		sessID := uuid.NewString()
		title := fmt.Sprintf("admin-e2e-session-%02d", i)
		if _, err := pool.Exec(ctx, `
			INSERT INTO sessions (id, tenant_id, title, labels, last_seq)
			VALUES ($1, $2, $3, '{}'::jsonb, 2)`,
			sessID, firstTenant, title,
		); err != nil {
			return fmt.Errorf("session: %w", err)
		}
		for seq := 1; seq <= 2; seq++ {
			if _, err := pool.Exec(ctx, `
				INSERT INTO session_events
					(session_id, seq, actor_type, actor_id, kind, payload)
				VALUES ($1, $2, 'service', 'seed', 'user_message', $3::jsonb)`,
				sessID, seq, `{"text":"hello"}`,
			); err != nil {
				return fmt.Errorf("event: %w", err)
			}
		}
		runID := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO agent_runs
				(id, session_id, tenant_id, state, loop_kind, loop_version,
				 model_config, prompt, history, grants, actor_kind, actor_id)
			VALUES
				($1, $2, $3, 'completed', 'default', 1,
				 '{}'::jsonb, 'seed', '{"v":1,"messages":[]}'::jsonb,
				 '{}'::jsonb, 'service', 'seed')`,
			runID, sessID, firstTenant,
		); err != nil {
			return fmt.Errorf("run: %w", err)
		}
	}

	fmt.Printf("seeded %d tenants; primary=%s canary_hash=%s\n",
		n, firstTenant, hex.EncodeToString(keyHash[:8]))
	return nil
}
