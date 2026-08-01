-- +goose Up

-- A stored flow version must read back byte-for-byte. jsonb normalizes
-- whitespace and key order, so a definition written as jsonb could come back
-- subtly different from what its author stored and what an invocation pinned.
-- Keep the source text and constrain it to be valid JSON.
ALTER TABLE flows
  ALTER COLUMN definition TYPE text USING definition::text,
  ADD CONSTRAINT flows_definition_is_json CHECK (definition::jsonb IS NOT NULL);

-- Flow invocations become a durable state machine rather than a receipt.
--
-- rendered freezes the exact prompts, grants, and environment specs the
-- invocation resolved, so a later flow version cannot change what an
-- in-flight or replayed invocation did. advance_at is the crash-safe polling
-- watermark: whichever worker claims it owns the next advance tick, which is
-- what keeps redelivery from multiplying polling chains.
ALTER TABLE flow_invocations
  ADD COLUMN rendered jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN state text NOT NULL DEFAULT 'pending',
  ADD COLUMN terminal_reason text NOT NULL DEFAULT '',
  ADD COLUMN message text NOT NULL DEFAULT '',
  ADD COLUMN cancel_requested_at timestamptz,
  ADD COLUMN advance_at timestamptz,
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
  ADD CONSTRAINT flow_invocations_state_check CHECK (state IN
    ('pending','provisioning','running','cancelling','completed','failed','cancelled'));

CREATE INDEX flow_invocations_session_idx ON flow_invocations(session_id, created_at DESC);
CREATE INDEX flow_invocations_active_idx ON flow_invocations(advance_at)
  WHERE state IN ('pending','provisioning','running','cancelling');

-- Progress is append-only and keyed. A redelivered advance job recomputes the
-- same key and is rejected by the unique index, so replay reconstructs one
-- ordered history rather than a multiplied one.
CREATE TABLE flow_invocation_progress (
  invocation_id uuid NOT NULL REFERENCES flow_invocations(id) ON DELETE CASCADE,
  seq bigint NOT NULL,
  stage text NOT NULL,
  key text NOT NULL,
  detail text NOT NULL DEFAULT '',
  at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (invocation_id, seq),
  UNIQUE (invocation_id, key)
);

-- Which flow declaration produced a run or an environment. Naming the
-- declaration (not just the invocation) is what lets cleanup, readiness, and
-- topology be scoped to exactly the resources a flow owns.
ALTER TABLE agent_runs ADD COLUMN flow_agent_name text NOT NULL DEFAULT '';
ALTER TABLE dev_envs
  ADD COLUMN flow_env_name text NOT NULL DEFAULT '';

-- One environment per (invocation, declared name): a retried provisioning
-- stage adopts the existing environment instead of creating a second one.
CREATE UNIQUE INDEX dev_envs_flow_declaration_idx
  ON dev_envs(flow_invocation_id, flow_env_name)
  WHERE flow_invocation_id IS NOT NULL;

-- One run per (invocation, declared agent name) for flow-launched agents, for
-- the same reason. Agents spawned at runtime from the flow catalog carry no
-- declaration name and are excluded.
CREATE UNIQUE INDEX agent_runs_flow_declaration_idx
  ON agent_runs(flow_invocation_id, flow_agent_name)
  WHERE flow_invocation_id IS NOT NULL AND flow_agent_name <> '';

-- +goose Down
ALTER TABLE flows
  DROP CONSTRAINT flows_definition_is_json,
  ALTER COLUMN definition TYPE jsonb USING definition::jsonb;
DROP INDEX agent_runs_flow_declaration_idx;
DROP INDEX dev_envs_flow_declaration_idx;
ALTER TABLE dev_envs DROP COLUMN flow_env_name;
ALTER TABLE agent_runs DROP COLUMN flow_agent_name;
DROP TABLE flow_invocation_progress;
DROP INDEX flow_invocations_active_idx;
DROP INDEX flow_invocations_session_idx;
ALTER TABLE flow_invocations
  DROP CONSTRAINT flow_invocations_state_check,
  DROP COLUMN updated_at,
  DROP COLUMN advance_at,
  DROP COLUMN cancel_requested_at,
  DROP COLUMN message,
  DROP COLUMN terminal_reason,
  DROP COLUMN state,
  DROP COLUMN rendered;
