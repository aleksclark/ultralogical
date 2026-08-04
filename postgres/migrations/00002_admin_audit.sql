-- +goose Up
-- E7: immutable operator audit trail for admin commands (additive only).

CREATE TABLE admin_audit_events (
    id               uuid PRIMARY KEY,
    ts               timestamptz NOT NULL DEFAULT now(),
    operator_id      text        NOT NULL,
    operator_role    text        NOT NULL,
    request_id       text        NOT NULL DEFAULT '',
    command          text        NOT NULL,
    targets          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    reason           text        NOT NULL DEFAULT '',
    preview_hash     text        NOT NULL DEFAULT '',
    before_summary   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    after_summary    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    result           text        NOT NULL,
    error            text        NOT NULL DEFAULT '',
    source_ip        text        NOT NULL DEFAULT '',
    build_version    text        NOT NULL DEFAULT '',
    idempotency_key  text
);

CREATE UNIQUE INDEX admin_audit_events_idempotency_uidx
    ON admin_audit_events (idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

CREATE INDEX admin_audit_events_ts_idx
    ON admin_audit_events (ts DESC);

CREATE INDEX admin_audit_events_command_ts_idx
    ON admin_audit_events (command, ts DESC);

CREATE INDEX admin_audit_events_operator_ts_idx
    ON admin_audit_events (operator_id, ts DESC);

CREATE INDEX admin_audit_events_result_ts_idx
    ON admin_audit_events (result, ts DESC);

-- +goose Down
DROP TABLE IF EXISTS admin_audit_events;
