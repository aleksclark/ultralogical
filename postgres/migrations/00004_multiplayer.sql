-- +goose Up
ALTER TABLE agent_runs
  ADD COLUMN parent_run_id uuid REFERENCES agent_runs(id),
  ADD COLUMN grants jsonb NOT NULL DEFAULT '{"tools":["*"],"env_all":true,"may_spawn":true,"max_children":16}',
  ADD COLUMN result jsonb;
CREATE INDEX agent_runs_parent_idx ON agent_runs(parent_run_id, created_at);

CREATE TABLE participants (
  session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('human','agent')),
  participant_id text NOT NULL,
  display text NOT NULL DEFAULT '',
  state text NOT NULL CHECK (state IN ('active','idle','left')),
  joined_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  left_at timestamptz,
  PRIMARY KEY(session_id, kind, participant_id)
);
CREATE INDEX participants_reap_idx ON participants(last_seen_at) WHERE state='active';

CREATE TABLE session_memory (
  session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  key text NOT NULL,
  value jsonb NOT NULL,
  updated_by_type text NOT NULL,
  updated_by_id text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(session_id,key)
);

CREATE TABLE run_waits (
  id uuid PRIMARY KEY,
  parent_run_id uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
  step_index int NOT NULL,
  tool_call_id text NOT NULL,
  state text NOT NULL DEFAULT 'open',
  deadline timestamptz NOT NULL,
  result jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz,
  UNIQUE(parent_run_id,step_index,tool_call_id)
);
CREATE TABLE run_wait_members (
  wait_id uuid NOT NULL REFERENCES run_waits(id) ON DELETE CASCADE,
  run_id uuid NOT NULL REFERENCES agent_runs(id),
  ordinal int NOT NULL,
  PRIMARY KEY(wait_id,run_id), UNIQUE(wait_id,ordinal)
);
CREATE INDEX run_wait_members_child_idx ON run_wait_members(run_id);

-- +goose Down
DROP TABLE run_wait_members;
DROP TABLE run_waits;
DROP TABLE session_memory;
DROP TABLE participants;
DROP INDEX agent_runs_parent_idx;
ALTER TABLE agent_runs DROP COLUMN result, DROP COLUMN grants, DROP COLUMN parent_run_id;
