# Phase E7 exit audit

## A7.1 Typed commands only
**Pass.** Mutations are exclusively `AdminCommandService` RPCs. No generic SQL,
shell, or HTTP proxy exists. Each command has typed request, authz, validation,
dry-run, idempotency key, bounded targets, and audit.

## A7.2 Role matrix
**Pass (pragmatic tokens).** Roles `viewer|operator|security|admin` with deny
default. Multi-token map and single-token+role supported. Tests cover viewer
denial and operator/security/admin allow paths. Server validates bearer on every
request (SPA short session is client-side only).

## A7.3 Stale preview fails closed
**Pass.** Execute recomputes preview hash from live before-state; mismatch →
`FailedPrecondition` + failed audit row. Covered by `TestE7_CancelRunDryRunExecuteIdempotentStale`.

## A7.4 Immutable searchable audit
**Pass.** `admin_audit_events` is append-only (no update/delete API). Search via
E5 list primitives (`ListAuditEvents` / SPA Audit page). Dry-run, success,
failure, and denied paths write rows.

## A7.5 Break-glass reveal
**Pass with kill switch default off.** Requires security|admin, reauth header,
reason, single target, short-lived response fields. Plaintext excluded from
audit JSON and redacting logger path exercised in tests. SPA modal warns and
does not persist/auto-copy secrets.

## A7.6 Admin isolation from runtime
**Pass.** Separate `coreadmin` binary/process; cored dependency fence and path
404 tests remain. Health-only process test shows client surface unaffected when
admin is absent. Rate/concurrency limits apply only to command engine.

## A7.7 Private exposure
**Pass (config posture).** Nomad admin group still has no Traefik tags; default
bind `127.0.0.1:8082`. CORS off unless SPA origin set. Public edge exposure is
an ops concern documented in runbooks.

## A7.8 Backup/restore / idempotency
**Pass (schema-level).** Audit table rides Postgres backup/restore. Unique
idempotency index prevents duplicate command application on key replay.
Operators must re-preview after restore before new executes.

## Threat notes
- Reveal requires master key on coreadmin — keep key distribution tight.
- `DisableCredential` deletes ciphertext row (metadata disable); not reversible via admin.
- `DisconnectSubscriber` deferred honestly rather than faked.
- AnswerAwait enqueues work; requires workers to progress runs.

## Verification commands
```sh
task admin:security:test
task admin:test
task admin:web:gate
task admin:web:build
go build ./...
```
