-- +goose Up
-- E4 baseline: final post-extraction schema. Additive-only after this file.
-- River queue tables are created by river's own migrator at process start.

CREATE TABLE tenants (
    id         uuid PRIMARY KEY,
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id         uuid PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id),
    name       text        NOT NULL DEFAULT '',
    scope      text        NOT NULL,
    prefix     text        NOT NULL,
    key_hash   bytea       NOT NULL UNIQUE,
    key_enc    bytea       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);
CREATE INDEX api_keys_tenant_idx ON api_keys (tenant_id, created_at DESC);

CREATE TABLE sessions (
    id          uuid PRIMARY KEY,
    tenant_id   uuid        NOT NULL REFERENCES tenants (id),
    title       text        NOT NULL DEFAULT '',
    labels      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    last_seq    bigint      NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);
CREATE INDEX sessions_tenant_idx ON sessions (tenant_id, created_at DESC);
CREATE INDEX sessions_labels_gin ON sessions USING gin (labels jsonb_path_ops);

CREATE TABLE session_events (
    session_id    uuid        NOT NULL REFERENCES sessions (id),
    seq           bigint      NOT NULL,
    ts            timestamptz NOT NULL DEFAULT now(),
    actor_type    text        NOT NULL, -- stores Actor.Kind (historical column name)
    actor_id      text        NOT NULL DEFAULT '',
    actor_display text        NOT NULL DEFAULT '',
    kind          text        NOT NULL,
    payload       jsonb       NOT NULL DEFAULT '{}',
    PRIMARY KEY (session_id, seq)
);

CREATE TABLE credentials (
    tenant_id   uuid        NOT NULL REFERENCES tenants (id),
    kind        text        NOT NULL,
    name        text        NOT NULL,
    enc_payload bytea       NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    rotated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, kind, name)
);

CREATE TABLE agent_runs (
    id                  uuid PRIMARY KEY,
    session_id          uuid        NOT NULL REFERENCES sessions (id),
    tenant_id           uuid        NOT NULL REFERENCES tenants (id),
    state               text        NOT NULL DEFAULT 'pending',
    loop_kind           text        NOT NULL,
    loop_version        int         NOT NULL,
    model_config        jsonb       NOT NULL,
    prompt              text        NOT NULL DEFAULT '',
    history             jsonb       NOT NULL DEFAULT '{"v":1,"messages":[]}',
    failure_reason      text        NOT NULL DEFAULT '',
    failure_message     text        NOT NULL DEFAULT '',
    cancel_requested_at timestamptz,
    parent_run_id       uuid REFERENCES agent_runs (id),
    -- policy JSON (column name retained for continuity with E1/E3 writers)
    grants              jsonb       NOT NULL DEFAULT '{}'::jsonb,
    result              jsonb,
    spawn_key           text,
    cohort_id           uuid,
    cohort_ordinal      int,
    actor_kind          text        NOT NULL DEFAULT '',
    actor_id            text        NOT NULL DEFAULT '',
    actor_display       text        NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_runs_session_idx ON agent_runs (session_id, created_at);
CREATE INDEX agent_runs_parent_idx ON agent_runs (parent_run_id, created_at);
CREATE UNIQUE INDEX agent_runs_spawn_key_idx ON agent_runs (tenant_id, spawn_key)
    WHERE spawn_key IS NOT NULL;
CREATE INDEX agent_runs_cohort_idx ON agent_runs (cohort_id, cohort_ordinal)
    WHERE cohort_id IS NOT NULL;

CREATE TABLE agent_run_steps (
    agent_run_id  uuid        NOT NULL REFERENCES agent_runs (id),
    step_index    int         NOT NULL,
    attempt       int         NOT NULL DEFAULT 1,
    tokens_in     bigint      NOT NULL DEFAULT 0,
    tokens_out    bigint      NOT NULL DEFAULT 0,
    finish_reason text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_run_id, step_index)
);

CREATE TABLE provider_instances (
    id              uuid PRIMARY KEY,
    tenant_id       uuid        NOT NULL REFERENCES tenants (id),
    kind            text        NOT NULL,
    name            text        NOT NULL,
    config          jsonb       NOT NULL DEFAULT '{}',
    state           text        NOT NULL DEFAULT 'ready',
    capabilities    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    last_healthy_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE TABLE resources (
    id                   uuid PRIMARY KEY,
    tenant_id            uuid        NOT NULL REFERENCES tenants (id),
    session_id           uuid        NOT NULL REFERENCES sessions (id),
    provider_instance_id uuid        NOT NULL REFERENCES provider_instances (id),
    kind                 text        NOT NULL DEFAULT 'dev_env',
    state                text        NOT NULL DEFAULT 'requested',
    spec                 jsonb       NOT NULL,
    handle               jsonb       NOT NULL DEFAULT '{"version":0,"data":null}',
    endpoint             text        NOT NULL DEFAULT '',
    token_hash           bytea       NOT NULL,
    token_enc            bytea       NOT NULL,
    epoch                int         NOT NULL DEFAULT 1,
    failure_message      text        NOT NULL DEFAULT '',
    created_by_run_id    uuid REFERENCES agent_runs (id),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    ready_at             timestamptz,
    terminated_at        timestamptz
);
CREATE INDEX resources_session_idx ON resources (tenant_id, session_id, created_at);
CREATE INDEX resources_active_idx ON resources (tenant_id, state)
    WHERE state NOT IN ('terminated', 'failed');
CREATE INDEX resources_kind_idx ON resources (tenant_id, session_id, kind);

CREATE TABLE session_memory (
    session_id      uuid        NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    key             text        NOT NULL,
    value           jsonb       NOT NULL,
    updated_by_type text        NOT NULL, -- stores Actor.Kind
    updated_by_id   text        NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, key)
);

CREATE TABLE run_waits (
    id             uuid PRIMARY KEY,
    parent_run_id  uuid        NOT NULL REFERENCES agent_runs (id) ON DELETE CASCADE,
    step_index     int         NOT NULL,
    tool_call_id   text        NOT NULL,
    kind           text        NOT NULL DEFAULT 'wait',
    state          text        NOT NULL DEFAULT 'open',
    timeout_policy text        NOT NULL DEFAULT 'resolve',
    deadline       timestamptz NOT NULL,
    result         jsonb,
    created_at     timestamptz NOT NULL DEFAULT now(),
    resolved_at    timestamptz,
    resumed_at     timestamptz,
    UNIQUE (parent_run_id, step_index, tool_call_id),
    CONSTRAINT run_waits_state_check
        CHECK (state IN ('open', 'resolved', 'timed_out', 'abandoned'))
);
CREATE INDEX run_waits_open_deadline_idx ON run_waits (deadline) WHERE state = 'open';
CREATE UNIQUE INDEX run_waits_one_open_per_parent_idx ON run_waits (parent_run_id)
    WHERE state = 'open';

CREATE TABLE run_wait_members (
    wait_id uuid NOT NULL REFERENCES run_waits (id) ON DELETE CASCADE,
    run_id  uuid NOT NULL REFERENCES agent_runs (id),
    ordinal int  NOT NULL,
    PRIMARY KEY (wait_id, run_id),
    UNIQUE (wait_id, ordinal)
);
CREATE INDEX run_wait_members_child_idx ON run_wait_members (run_id);

CREATE TABLE hook_cursors (
    session_id uuid   NOT NULL REFERENCES sessions (id),
    hook       text   NOT NULL,
    last_seq   bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (session_id, hook)
);

CREATE TABLE periodic_prompts (
    id         uuid PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id),
    session_id uuid        NOT NULL REFERENCES sessions (id),
    run_id     uuid REFERENCES agent_runs (id),
    schedule   text        NOT NULL,
    prompt     text        NOT NULL,
    enabled    boolean     NOT NULL DEFAULT true,
    next_at    timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS periodic_prompts;
DROP TABLE IF EXISTS hook_cursors;
DROP TABLE IF EXISTS run_wait_members;
DROP TABLE IF EXISTS run_waits;
DROP TABLE IF EXISTS session_memory;
DROP TABLE IF EXISTS resources;
DROP TABLE IF EXISTS provider_instances;
DROP TABLE IF EXISTS agent_run_steps;
DROP TABLE IF EXISTS agent_runs;
DROP TABLE IF EXISTS credentials;
DROP TABLE IF EXISTS session_events;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS tenants;
