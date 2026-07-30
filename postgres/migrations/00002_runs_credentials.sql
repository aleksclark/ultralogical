-- +goose Up
CREATE TABLE credentials (
    org_id     uuid        NOT NULL REFERENCES orgs (id),
    kind       text        NOT NULL,
    name       text        NOT NULL,
    enc_payload bytea      NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    rotated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, kind, name)
);

CREATE TABLE agent_runs (
    id                  uuid PRIMARY KEY,
    session_id          uuid        NOT NULL REFERENCES sessions (id),
    org_id              uuid        NOT NULL REFERENCES orgs (id),
    state               text        NOT NULL DEFAULT 'pending',
    loop_kind           text        NOT NULL,
    loop_version        int         NOT NULL,
    model_config        jsonb       NOT NULL,
    prompt              text        NOT NULL DEFAULT '',
    history             jsonb       NOT NULL DEFAULT '{"v":1,"messages":[]}',
    failure_reason      text        NOT NULL DEFAULT '',
    failure_message     text        NOT NULL DEFAULT '',
    cancel_requested_at timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX agent_runs_session_idx ON agent_runs (session_id, created_at);

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

-- +goose Down
DROP TABLE agent_run_steps;
DROP TABLE agent_runs;
DROP TABLE credentials;
