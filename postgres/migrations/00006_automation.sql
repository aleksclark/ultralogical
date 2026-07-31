-- +goose Up
CREATE TABLE hook_cursors(session_id uuid NOT NULL REFERENCES sessions(id), hook text NOT NULL, last_seq bigint NOT NULL DEFAULT 0, PRIMARY KEY(session_id,hook));
CREATE TABLE periodic_prompts(id uuid PRIMARY KEY,org_id uuid NOT NULL REFERENCES orgs(id),session_id uuid NOT NULL REFERENCES sessions(id),run_id uuid REFERENCES agent_runs(id),schedule text NOT NULL,prompt text NOT NULL,enabled boolean NOT NULL DEFAULT true,next_at timestamptz NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
-- +goose Down
DROP TABLE periodic_prompts;
DROP TABLE hook_cursors;
