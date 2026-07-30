-- +goose Up
CREATE TABLE provider_instances (
    id              uuid PRIMARY KEY,
    org_id          uuid        NOT NULL REFERENCES orgs (id),
    kind            text        NOT NULL,
    name            text        NOT NULL,
    config          jsonb       NOT NULL DEFAULT '{}',
    rate_class      text        NOT NULL DEFAULT 'byo',
    state           text        NOT NULL DEFAULT 'ready',
    last_healthy_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

CREATE TABLE dev_envs (
    id                   uuid PRIMARY KEY,
    org_id               uuid        NOT NULL REFERENCES orgs (id),
    session_id           uuid        NOT NULL REFERENCES sessions (id),
    provider_instance_id uuid        NOT NULL REFERENCES provider_instances (id),
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
CREATE INDEX dev_envs_session_idx ON dev_envs (org_id, session_id, created_at);
CREATE INDEX dev_envs_active_idx ON dev_envs (org_id, state) WHERE state NOT IN ('terminated', 'failed');

CREATE TABLE env_usage (
    id                   uuid PRIMARY KEY,
    org_id               uuid        NOT NULL REFERENCES orgs (id),
    env_id               uuid        NOT NULL REFERENCES dev_envs (id),
    provider_instance_id uuid        NOT NULL REFERENCES provider_instances (id),
    started_at           timestamptz NOT NULL,
    last_metered_at      timestamptz NOT NULL,
    ended_at             timestamptz,
    seconds              bigint      NOT NULL DEFAULT 0,
    rate_class           text        NOT NULL,
    correction_of        uuid REFERENCES env_usage (id)
);
CREATE UNIQUE INDEX env_usage_one_open_idx ON env_usage (env_id) WHERE ended_at IS NULL;
CREATE INDEX env_usage_org_period_idx ON env_usage (org_id, started_at);

-- +goose Down
DROP TABLE env_usage;
DROP TABLE dev_envs;
DROP TABLE provider_instances;
