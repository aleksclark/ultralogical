-- +goose Up

-- Spawn idempotency. A spawn is identified by the parent run, the step that
-- issued it, and the model's tool-call id. Queue redelivery replays the same
-- tool call, so this key is what makes "one child per spawn" a database fact
-- rather than a hope.
ALTER TABLE agent_runs
  ADD COLUMN spawn_key text,
  ADD COLUMN cohort_id uuid,
  ADD COLUMN cohort_ordinal int;
CREATE UNIQUE INDEX agent_runs_spawn_key_idx ON agent_runs(org_id, spawn_key)
  WHERE spawn_key IS NOT NULL;
CREATE INDEX agent_runs_cohort_idx ON agent_runs(cohort_id, cohort_ordinal)
  WHERE cohort_id IS NOT NULL;

-- Wait state machine. A wait is open, then exactly one of resolved (all
-- members terminal), timed_out (deadline passed first), or abandoned (the
-- parent went terminal). resumed_at records that the parent's next step was
-- enqueued, so at-most-once resumption is enforced by this row and not by any
-- in-memory flag.
ALTER TABLE run_waits
  DROP CONSTRAINT IF EXISTS run_waits_state_check;
ALTER TABLE run_waits
  ADD COLUMN kind text NOT NULL DEFAULT 'wait',
  ADD COLUMN timeout_policy text NOT NULL DEFAULT 'resolve',
  ADD COLUMN resumed_at timestamptz,
  ADD CONSTRAINT run_waits_state_check
    CHECK (state IN ('open','resolved','timed_out','abandoned'));

-- Open waits are polled by the timeout sweeper, which must find due rows
-- cheaply and must never see a resolved one.
CREATE INDEX run_waits_open_deadline_idx ON run_waits(deadline)
  WHERE state = 'open';

-- A parent may hold at most one open wait at a time: a second concurrent wait
-- would make "resume once" ambiguous.
CREATE UNIQUE INDEX run_waits_one_open_per_parent_idx ON run_waits(parent_run_id)
  WHERE state = 'open';

-- +goose Down
DROP INDEX run_waits_one_open_per_parent_idx;
DROP INDEX run_waits_open_deadline_idx;
ALTER TABLE run_waits
  DROP CONSTRAINT run_waits_state_check,
  DROP COLUMN resumed_at,
  DROP COLUMN timeout_policy,
  DROP COLUMN kind;
DROP INDEX agent_runs_cohort_idx;
DROP INDEX agent_runs_spawn_key_idx;
ALTER TABLE agent_runs
  DROP COLUMN cohort_ordinal,
  DROP COLUMN cohort_id,
  DROP COLUMN spawn_key;
