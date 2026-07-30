-- +goose Up
CREATE TABLE orgs (
    id         uuid PRIMARY KEY,
    name       text        NOT NULL,
    plan       text        NOT NULL DEFAULT 'dev',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id         uuid PRIMARY KEY,
    email      text        NOT NULL UNIQUE,
    display    text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE org_members (
    org_id    uuid        NOT NULL REFERENCES orgs (id),
    user_id   uuid        NOT NULL REFERENCES users (id),
    role      text        NOT NULL,
    joined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);

CREATE TABLE sessions (
    id          uuid PRIMARY KEY,
    org_id      uuid        NOT NULL REFERENCES orgs (id),
    title       text        NOT NULL DEFAULT '',
    last_seq    bigint      NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);

CREATE INDEX sessions_org_idx ON sessions (org_id, created_at DESC);

CREATE TABLE session_events (
    session_id uuid        NOT NULL REFERENCES sessions (id),
    seq        bigint      NOT NULL,
    ts         timestamptz NOT NULL DEFAULT now(),
    actor_type text        NOT NULL,
    actor_id   text        NOT NULL DEFAULT '',
    kind       text        NOT NULL,
    payload    jsonb       NOT NULL DEFAULT '{}',
    PRIMARY KEY (session_id, seq)
);

-- +goose Down
DROP TABLE session_events;
DROP TABLE sessions;
DROP TABLE org_members;
DROP TABLE users;
DROP TABLE orgs;
