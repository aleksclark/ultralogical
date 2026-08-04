# Phase E7 exit audit

## Independent review (post-CI green)

Material safety gaps found and fixed on this pass:

1. **Post-apply stale-preview check** — `Run` recomputed the preview hash
   *after* `exec(false)` and could return `FailedPrecondition` for a command
   that had already mutated. Removed the post-apply check; stale confirmation
   is validated against live before-state *before* any mutation. In-command
   `GetForUpdate` / terminal-state checks handle races.
2. **Failed attempts poisoned idempotency keys** — failed/stale/denied rows
   wrote `idempotency_key`, so a later retry with the same key short-circuited
   to the failure. Keys now bind only `ok` / `already_applied` outcomes;
   failures still audit without binding the key.
3. **Reveal kill switch soft-denied** — disabled reveal returned
   `FailedPrecondition`. Kill switch now returns `Unimplemented` so the RPC
   is treated as absent when `CORE_ADMIN_REVEAL_ENABLED=false`.
4. **Audit immutability was API-only** — added DB triggers that reject
   `UPDATE`/`DELETE` on `admin_audit_events` (goose `StatementBegin` wrapped).
5. **Idempotency lookup** — uses `pgx.ErrNoRows` correctly and filters to
   successful results only.

New coverage: `TestE7_StalePreviewDoesNotMutateAndFailedIdempotencyRetries`,
DB-side immutability asserts in `TestE7_AuditImmutableNoDeleteAPI`.

## A7.1 Typed commands only
**Pass.** Mutations are exclusively `AdminCommandService` RPCs. No generic SQL,
shell, or HTTP proxy exists. Each command has typed request, authz, validation,
dry-run, idempotency key, bounded targets, and audit.

## A7.2 Role matrix
**Pass (pragmatic tokens).** Roles `viewer|operator|security|admin` with deny
default. Multi-token map and single-token+role supported. Tests cover viewer
denial and operator/security/admin allow paths. Server validates bearer on every
request (SPA short session is client-side only).

Evidence: `TestE7_WhoAmIAndRoleMatrix`, `authz.Can` deny-default.

## A7.3 Stale preview fails closed
**Pass.** Execute recomputes preview hash from live before-state *before*
mutation; mismatch → `FailedPrecondition` + failed audit row (no idempotency
binding). Covered by `TestE7_CancelRunDryRunExecuteIdempotentStale` and
`TestE7_StalePreviewDoesNotMutateAndFailedIdempotencyRetries`.

## A7.4 Immutable searchable audit
**Pass.** `admin_audit_events` is append-only at the DB layer (UPDATE/DELETE
triggers) and has no update/delete API. Search via E5 list primitives
(`ListAuditEvents` / SPA Audit page). Dry-run, success, failure, and denied
paths write rows. Evidence: `TestE7_AuditImmutableNoDeleteAPI`.

## A7.5 Break-glass reveal
**Pass with kill switch default off.** Requires security|admin, reauth header,
reason, single target, short-lived response fields. Kill switch returns
`Unimplemented`. Plaintext excluded from audit JSON and redacting logger path
exercised in tests. SPA modal warns and does not persist/auto-copy secrets.

Evidence: `TestE7_RevealKillSwitchRoleAndNoPlaintextLogs`.

## A7.6 Admin isolation from runtime
**Pass.** Separate `coreadmin` binary/process; cored dependency fence and path
404 tests remain. Health-only process test shows client surface unaffected when
admin is absent. Rate/concurrency limits apply only to command engine.
`scripts/check-admin-isolation.sh` green.

## A7.7 Private exposure
**Pass (config posture).** Nomad admin group still has no Traefik tags; default
bind `127.0.0.1:8082`. CORS off unless SPA origin set. Public edge exposure is
an ops concern documented in runbooks.

## A7.8 Backup/restore / idempotency
**Pass (schema-level + engine).** Audit table rides Postgres backup/restore.
Unique idempotency index prevents duplicate *successful* command application on
key replay. Failed attempts do not reserve the key. Operators must re-preview
after restore before new executes.

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
bash scripts/check-admin-isolation.sh
```

## Verdict

Phase E7 is **complete** for the acceptance bar after the independent review
fixes above. Remaining items are intentional deferrals (fleet IdP/mTLS,
DisconnectSubscriber bus handle, full network provider re-probe).
