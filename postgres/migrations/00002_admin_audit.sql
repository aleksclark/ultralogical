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

-- Immutability: operators (and the application role) cannot edit or delete
-- audit rows. Enforcement is DB-side so a compromised coreadmin binary still
-- cannot rewrite history through SQL it issues.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION admin_audit_events_immutability()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'admin_audit_events is append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER admin_audit_events_no_update
    BEFORE UPDATE ON admin_audit_events
    FOR EACH ROW
    EXECUTE FUNCTION admin_audit_events_immutability();

CREATE TRIGGER admin_audit_events_no_delete
    BEFORE DELETE ON admin_audit_events
    FOR EACH ROW
    EXECUTE FUNCTION admin_audit_events_immutability();

-- +goose Down
DROP TRIGGER IF EXISTS admin_audit_events_no_delete ON admin_audit_events;
DROP TRIGGER IF EXISTS admin_audit_events_no_update ON admin_audit_events;
DROP FUNCTION IF EXISTS admin_audit_events_immutability();
DROP TABLE IF EXISTS admin_audit_events;
