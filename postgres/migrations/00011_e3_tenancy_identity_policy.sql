-- +goose Up
-- E3: API keys, session labels, opaque actor columns, run policy storage.
-- Column name org_id is retained until the E4 squash renames tables/columns.

CREATE TABLE api_keys (
    id         uuid PRIMARY KEY,
    org_id     uuid        NOT NULL REFERENCES orgs (id),
    name       text        NOT NULL DEFAULT '',
    scope      text        NOT NULL,
    prefix     text        NOT NULL,
    key_hash   bytea       NOT NULL UNIQUE,
    key_enc    bytea       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);

CREATE INDEX api_keys_org_idx ON api_keys (org_id, created_at DESC);

-- Session labels as jsonb for GIN containment queries.
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS labels jsonb NOT NULL DEFAULT '{}'::jsonb;
CREATE INDEX IF NOT EXISTS sessions_labels_gin ON sessions USING gin (labels jsonb_path_ops);

-- Opaque actor attribution on the event log (kind replaces type; display added).
ALTER TABLE session_events ADD COLUMN IF NOT EXISTS actor_display text NOT NULL DEFAULT '';
-- actor_type column keeps storing Actor.Kind (historical column name).

-- Run actor + keep grants column as the policy JSON blob (shape changes in app).
ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS actor_kind text NOT NULL DEFAULT '';
ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS actor_id text NOT NULL DEFAULT '';
ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS actor_display text NOT NULL DEFAULT '';

-- Session memory stores actor kind in updated_by_type (historical column name).

-- +goose Down
ALTER TABLE agent_runs DROP COLUMN IF EXISTS actor_display;
ALTER TABLE agent_runs DROP COLUMN IF EXISTS actor_id;
ALTER TABLE agent_runs DROP COLUMN IF EXISTS actor_kind;
ALTER TABLE session_events DROP COLUMN IF EXISTS actor_display;
DROP INDEX IF EXISTS sessions_labels_gin;
ALTER TABLE sessions DROP COLUMN IF EXISTS labels;
DROP TABLE IF EXISTS api_keys;
