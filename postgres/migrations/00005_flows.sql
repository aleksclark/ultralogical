-- +goose Up
CREATE TABLE flows (
 id uuid PRIMARY KEY, org_id uuid NOT NULL REFERENCES orgs(id), name text NOT NULL,
 version int NOT NULL, definition jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(org_id,name,version)
);
CREATE TABLE flow_invocations (
 id uuid PRIMARY KEY, org_id uuid NOT NULL REFERENCES orgs(id), session_id uuid NOT NULL REFERENCES sessions(id),
 flow_id uuid NOT NULL REFERENCES flows(id), flow_name text NOT NULL, flow_version int NOT NULL,
 params jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE agent_runs ADD COLUMN flow_invocation_id uuid REFERENCES flow_invocations(id);
ALTER TABLE dev_envs ADD COLUMN flow_invocation_id uuid REFERENCES flow_invocations(id);
-- +goose Down
ALTER TABLE dev_envs DROP COLUMN flow_invocation_id;
ALTER TABLE agent_runs DROP COLUMN flow_invocation_id;
DROP TABLE flow_invocations;
DROP TABLE flows;
