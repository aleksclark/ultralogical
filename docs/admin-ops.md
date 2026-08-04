# Admin operations runbooks (E7)

Private operator mutations live on `coreadmin` only (`AdminCommandService`).
They are typed, dry-runnable, idempotent, role-gated, and immutably audited.
There is **no** generic SQL/shell/HTTP proxy.

## Roles

| Role | Mutate? | Reveal? |
|---|---|---|
| `viewer` | no | no |
| `operator` | yes (catalog) | no |
| `security` | yes + revoke/disable + reveal | yes |
| `admin` | all | yes |

Deny by default. Configure:

```bash
# Multi-token (preferred for fleet / tests)
export CORE_ADMIN_TOKENS='{"op-token":{"role":"operator","name":"ops"},"view-token":{"role":"viewer","name":"ro"},"sec-token":{"role":"security","name":"sec"}}'

# Or single token
export CORE_ADMIN_TOKEN=...
export CORE_ADMIN_TOKEN_ROLE=admin   # default admin
```

## Command contract

1. **Preview** (`dry_run=true`) → `preview_hash`, before summary, expected effects.
2. **Execute** requires `preview_hash` + `idempotency_key` + `reason`.
3. Stale preview (targets changed) → `FailedPrecondition`.
4. Idempotent replay returns the original audit-backed outcome.
5. Every attempt writes `admin_audit_events` (including dry-run / denied / failed).

Destructive gates (default **off**):

| Flag | Commands |
|---|---|
| `CORE_ADMIN_ENABLE_TERMINATE=false` | `ResourceTerminate` |
| `CORE_ADMIN_ENABLE_SUSPEND=false` | `ResourceSuspend` |
| `CORE_ADMIN_ENABLE_DISCONNECT_SUBSCRIBER=false` | `DisconnectSubscriber` (also unsupported without bus handle) |
| `CORE_ADMIN_REVEAL_ENABLED=false` | `RevealSecret` kill switch |

Rate limits: `CORE_ADMIN_CMD_RATE_LIMIT` (default 20/s), `CORE_ADMIN_CMD_CONCURRENCY` (default 8).

## Catalog

| Command | Effect |
|---|---|
| `RetryQueueJob` / `CancelQueueJob` | River job retry/cancel |
| `CancelRun` | Request cancel; finalize if awaiting |
| `AnswerAwait` | Operator answer + enqueue next step |
| `ExpireAwait` | Force-cancel awaiting run |
| `ResourceReconcile` | Enqueue `resource.reconcile` |
| `ResourceRestart` | Restart path / enqueue restart |
| `ResourceSuspend` | Set suspended (flagged) |
| `ResourceTerminate` | Terminate path (flagged) |
| `ResourceAdoptionProbe` | Probe metadata + enqueue reconcile |
| `ReprobeProvider` | `MarkHealthy` metadata refresh |
| `RevokeAPIKey` | Set `revoked_at` |
| `DisableCredential` | Delete credential row (metadata) |
| `PausePeriodicPrompt` / `ResumePeriodicPrompt` | Toggle `enabled` |
| `DisconnectSubscriber` | Deferred — no cross-process bus handle |
| `ExportIncidentEvidence` | Bounded JSON bundle (no secret plaintext) |
| `RevealSecret` | Break-glass decrypt (see below) |

Forbidden forever: event-log rewrite, seq mutation, direct ciphertext rewrite,
generic table updates.

## Break-glass reveal

1. `CORE_ADMIN_REVEAL_ENABLED=true` and `CORE_MASTER_KEY` on **coreadmin only**.
2. Caller role `security` or `admin`.
3. Header `X-Admin-Reauth: <same bearer token>`.
4. Single target (`api_key` **or** one credential).
5. Incident `reason` required on execute.
6. Plaintext returned once in RPC body; **never** written to logs, audit JSON,
   URLs, or SPA `localStorage`. SPA shows a warning and does not auto-copy.

## Incident runbooks

### Compromised operator token

1. Rotate `CORE_ADMIN_TOKEN` / remove entry from `CORE_ADMIN_TOKENS`; restart coreadmin.
2. Search `/audit` for `operator_id` / time window.
3. Review `RevokeAPIKey`, `RevealSecret`, terminate/restart commands.
4. If reveal occurred, rotate the revealed tenant secret and any downstream tokens.

### Accidental command

1. Find audit row by `idempotency_key` or `request_id`.
2. Use before/after summaries to determine actual effect.
3. Compensating actions are themselves typed commands (e.g. `ResumePeriodicPrompt`).
4. Do **not** hand-edit Postgres to “undo” event history.

### Private route exposure

1. Confirm Nomad admin group has **no** Traefik tags; bind remains private.
2. `cored` must 404 admin paths (`task admin:security:test`).
3. Revoke exposed tokens; treat as compromised operator credential.

### Admin API overload

1. coreadmin is a separate process — cored/coreworker continue.
2. Lower `CORE_ADMIN_CMD_RATE_LIMIT` / concurrency; scale admin group independently.
3. Client API health is independent of admin readiness.

## Backup / restore

- `admin_audit_events` is part of the Postgres dataset; restore preserves history.
- Idempotency unique index prevents duplicate side effects on replay of the same key.
- After restore, re-issue previews before any new execute (hashes are state-bound).

## Tests

```sh
task admin:security:test
task admin:test
task admin:web:test
```
