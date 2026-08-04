# Phase E7 — Admin operations, audit, and production hardening

**Objective:** Add carefully bounded operational actions to the private admin
surface, with mandatory dry-run/confirmation, immutable audit evidence, and
production-grade access controls. Read-only debugging from E5/E6 remains the
default posture.

**Depends on:** E6.

---

## Scope

- Operator roles and short-lived sessions.
- High-value repair/retry/cancel controls through explicit admin commands.
- Break-glass secret reveal only where operationally unavoidable.
- Immutable operator audit log and complete before/after evidence.
- Disaster-safe rollout, backup/restore, and incident workflows.

## Safety model

- Admin mutation RPCs live in a separate `AdminCommandService`; no generic
  SQL, arbitrary table update, arbitrary shell, or arbitrary HTTP proxy.
- Every command has a typed request, authorization rule, validation, dry-run
  response, idempotency key, bounded target set, and auditable result.
- The SPA requires an explicit confirmation step showing exact targets and
  expected effects. Bulk actions require a typed confirmation phrase.
- Commands revalidate state at execution time. Stale previews fail rather than
  applying to changed targets.
- Destructive commands are disabled by default in deployment config and may be
  enabled individually.

## Work items

### T7.1 — Operator identity and authorization

- Integrate the fleet identity provider or mTLS identities.
- Roles: `viewer`, `operator`, `security`, `admin`; deny by default.
- Short session lifetimes, CSRF protection, origin checks, session revocation,
  and no credentials in local storage.
- Separate permissions per command and secret reveal.

### T7.2 — Typed operational commands

Initial command catalog:

- retry/cancel a queue job or failed step;
- cancel a run and answer/expire an awaiting run;
- trigger resource reconcile, restart, suspend, terminate, or adoption probe;
- re-probe a provider and refresh capabilities;
- disable/revoke an API key or credential metadata record;
- pause/resume a periodic prompt;
- disconnect a stale subscriber;
- export bounded incident evidence for a session/run/resource.

Commands that could violate event-log invariants, rewrite seq, delete events,
or mutate encrypted values directly are forbidden.

### T7.3 — Immutable audit trail

- Record operator identity, request ID, command, targets, reason, preview hash,
  before/after summaries, result, timestamps, source IP, and deployment build.
- Append audit records transactionally with core state changes where possible;
  otherwise record started/completed/failed phases with correlation IDs.
- Audit records are searchable/paginated through E5 primitives and visible in
  E6 screens. Operators cannot edit or delete them.

### T7.4 — Break-glass secret reveal

- Default remains metadata/ciphertext diagnostics only.
- Reveal requires `security` role, recent re-authentication, incident reason,
  single-record target, short-lived response, and a dedicated audit event.
- Never write revealed plaintext to logs, traces, URLs, browser storage,
  downloads, screenshots, clipboard automatically, or error reporting.
- Add deployment kill switch that removes the reveal RPC entirely.

### T7.5 — Production hardening

- Rate limits, query budgets, concurrent-operation limits, request size limits,
  and circuit breakers independent of client API limits.
- Admin API and SPA availability must not be required for cored/coreworker.
- Backup/restore proof includes admin audit data.
- Incident runbooks cover compromised operator credential, accidental command,
  private-route exposure, and admin API overload.
- Fleet deploy proves private routing and rollback independently of cored.

---

## Acceptance criteria

- **A7.1** No generic mutation primitive exists. Every command is individually
  typed, permissioned, dry-runnable, idempotent, bounded, and audited.
- **A7.2** Viewer cannot mutate; operator/security/admin permissions match the
  catalog; expired/revoked sessions fail closed mid-operation.
- **A7.3** Preview hash/state-version mismatch prevents stale confirmation from
  applying after targets change.
- **A7.4** Every successful and failed command produces immutable searchable
  audit evidence with operator, reason, target, before/after, and correlation.
- **A7.5** Break-glass reveal meets all T7.4 controls and plaintext leakage
  scans remain clean.
- **A7.6** Admin outage/overload leaves client API, event append, loop jobs, and
  workers healthy; separate process/resource limits are proven under load.
- **A7.7** Public network exposure scans cannot reach admin SPA/API; private
  operator access works through the fleet's intended private path.
- **A7.8** Backup/restore and rollback preserve audit history and do not cause
  duplicate command effects.

## Exit audit

`phase_e7_audit.md` threat-models every command, independently verifies the
role matrix and audit completeness, exercises stale-preview and duplicate
submission races, scans for secret leakage, and proves admin failure isolation
from the runtime.
