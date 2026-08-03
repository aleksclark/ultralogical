-- +goose Up
ALTER TABLE dev_envs ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'dev_env';
CREATE INDEX IF NOT EXISTS dev_envs_kind_idx ON dev_envs (org_id, session_id, kind);
-- +goose Down
DROP INDEX IF EXISTS dev_envs_kind_idx;
ALTER TABLE dev_envs DROP COLUMN IF EXISTS kind;
