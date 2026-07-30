# Phase 7 — Production hardening, billing & queue-swap proof

**Duration:** 2–3 weeks · **Depends on:** Phase 6

## Goal

Make the system operable and sellable in production: real authn/z, **billing on the
metering ledger (seats + env usage via Stripe)**, backpressure, retention, graceful
deploys, chaos-tested durability at load — and cash the "queue backend easily swapped
while remaining type-safe" check by shipping a second real backend through the
conformance suite and the full functional suite.

## Scope

**In:**
- AuthN: pluggable OIDC (interface + one provider impl) for humans; per-participant
  API tokens; run/env tokens unchanged (already scoped).
- AuthZ: org-membership + session-membership enforcement on every RPC; role model v1
  (org: owner/admin/member; session: owner/member/observer); observer = subscribe-only.
- **Billing** (`/billing`, Stripe behind an interface): seat subscriptions (per active
  `org_members` row per month, tiered plans), metered env usage (monthly aggregation
  of `env_usage.seconds` by rate class → Stripe metered items), checkout/portal
  flows, webhook ingestion (subscription state → `orgs.plan`), grace/dunning v1
  (past-due → warning banner → provision-freeze after N days; sessions/history remain
  readable — we never hold data hostage), plan quotas (seats, concurrent envs,
  hosted-EKS access) enforced with typed errors.
- Queue swap proof: `jobqueue/pgq` — a Postgres-native `FOR UPDATE SKIP LOCKED`
  implementation passing conformance + full functional suite via `ULTRA_QUEUE=pg`
  CI matrix leg.
- Event log operations: retention policy (archive to object storage, tombstone in PG),
  `Subscribe` backpressure (bounded per-subscriber buffer, slow-consumer disconnect
  with resume-by-seq), `Range` pagination hardening.
- Graceful lifecycle: ultrad drain (finish streams with a GOAWAY-style event, clients
  auto-resume), worker drain (finish current step, stop claiming), health/readiness
  endpoints, connection budgets (pgx pools sized; river + listeners accounted).
- Observability: dashboards (queue depth, step latency, event fan-out lag, env
  provision times, LLM error rates), alert rules, structured logs with
  session/run/step correlation.
- Load & chaos test suites (tagged, CI-scheduled).
- Ops docs + runbooks.
- UI: billing page (plan, seats, usage-to-date, checkout/portal links), admin usage
  dashboards per org.

**Out:** multi-region, billing beyond Stripe (invoicing/PO), usage-based seat
proration, enterprise SSO/SCIM.

## Design details

### Auth

- `ultra.Authenticator` (root-package seam): OIDC impl first (verify JWT, map to user →
  org memberships); static-token impl retained for dev/harness.
- Every RPC handler resolves `(identity, org, session, role)`; a table-driven policy
  maps RPC → minimum role (e.g. `Subscribe`: observer; `Append`/`StartRun`: member;
  `ArchiveSession`/role management: session owner; credential/provider/billing
  management: org admin). Deny → typed `PermissionDenied` RPC error (and, for
  in-session actions, the Phase 3 event for visibility).
- Run tokens and env tokens are unchanged; this phase adds the *human* edge.

### Billing pipeline

- A nightly (and on-demand) aggregation job folds `env_usage` intervals into
  per-org-per-period usage records (idempotent by `(org, period, rate_class)` upsert),
  pushed to Stripe metered items. The ledger stays authoritative: Stripe is an
  invoicing renderer, and every line item is re-derivable from `env_usage` — which is
  itself derived from events the user can replay in their own sessions.
- Seat counting: active members at period close (v1 simplicity; proration is a
  non-goal). Webhooks update `orgs.plan` + subscription state; enforcement reads only
  our columns (Stripe outage degrades to "no plan changes", never to lockout).

### jobqueue/pgq

- Plain-Postgres implementation: jobs table, `FOR UPDATE SKIP LOCKED` claim loop,
  visibility timeout, attempt counting, exponential backoff columns, transactional
  enqueue via the same `EnqueueTx(tx, ...)` seam. Deliberately boring (~500 LOC).
- Purpose is proof, not replacement: river stays default. The CI matrix leg running
  the **entire functional suite** with `ULTRA_QUEUE=pg` is the strongest possible
  statement that the seam holds — if it passes, any future backend (SQS, NATS
  JetStream) only needs conformance + matrix.

### Subscribe backpressure

- Per-subscriber ring buffer (configurable, default 1024 events). Overflow → server
  closes the stream with a typed `ResumeRequired{last_delivered_seq}` status; clients
  (testclient, TS, rust) implement auto-resume from that seq. This keeps one slow
  consumer from ballooning memory while never losing data (the log is the buffer).

### Retention

- Policy per deployment: events older than N days for archived sessions → batched to
  object storage (JSONL per session, seq-contiguous), rows replaced by a
  `range tombstone` marker. `Subscribe from_seq` inside an archived range returns a
  typed error directing to the archive export API. Active sessions never archived.

### Chaos harness

`testkit/chaos`: composable fault injectors over the multi-replica harness — worker
killer (SIGKILL every Ns), ultrad rolling restarter, Postgres connection dropper
(proxy-based), env container killer. Scenarios assert the same invariants the
functional suite asserts (no duplicate steps, no event gaps, terminal states reached,
no orphaned envs), just under fire.

## Work breakdown

0. **Define and implement capability evidence first.** Inventory every public behavior this phase will add/change (success, failure, replay/reconnect, tenancy, and distributed failure where applicable). Create capability-specific Go real-stack, Playwright web, and Rust desktop tests using shipped client/application paths. Add `e2e/coverage.json` references only after those existing tests assert observable outcomes. Smoke tests, control presence, unasserted RPCs, test-only shortcuts, and nonexistent filenames are not evidence.

1. Authenticator interface + OIDC impl + role policy table + enforcement + tests.
2. Billing: Stripe adapter, aggregation job, checkout/portal/webhooks, quotas +
   dunning states, billing UI.
3. `jobqueue/pgq` + conformance + CI matrix leg.
4. Backpressure + client auto-resume (Go, TS) + tests.
5. Retention/archival job + export API + tests.
6. Drain paths + readiness endpoints + connection budget audit.
7. Dashboards + alerts (as code) + structured log correlation audit.
8. Chaos suite + load suite.
9. Runbooks (`docs/ops/`): deploy, scale, incident (stuck runs, queue backlog,
   env leaks, billing disputes), backup/restore.

## Acceptance tests

- **A7.1 — Queue seam proof.** The full functional suite (Phases 0–6 tests) passes
  with `ULTRA_QUEUE=pg` as a CI matrix leg. jobqueue conformance passes on river,
  inproc, and pgq.
- **A7.2 — Chaos.** 20 concurrent scripted runs (mixed: env work, cohorts, awaiting);
  a worker is SIGKILLed every 10s for 2 minutes; one ultrad replica restarted
  mid-test. Assert: all runs reach correct terminal states; zero duplicate
  `(run, step_index)`; zero event-log gaps per session; zero orphaned env containers
  after a final reconcile; all subscriber clients converge to identical logs.
- **A7.3 — AuthZ + tenancy sweep.** Table test over **every** RPC in the proto (the
  table is generated from the proto descriptor, so a new RPC without a policy entry
  fails the build): unauthenticated → denied; authenticated non-member → denied;
  observer attempting mutations → denied; member/owner paths succeed; org-admin-only
  RPCs (credentials, providers, billing) denied to plain members. **Cross-tenant
  fuzz**: for every RPC with an ID field, IDs belonging to another org (sessions,
  runs, envs, flows, credentials, instances, usage) — including nested fields — are
  denied indistinguishably from not-found.
- **A7.4 — Zero-loss rolling deploy.** A subscriber streams continuously while the
  harness rolling-restarts both ultrad replicas. Assert: the client's auto-resume
  (from_seq) yields a complete, gap-free, duplicate-free event sequence; in-flight
  runs are unaffected; StartRun requests during the roll succeed (retried by client
  policy) or fail crisply — none hang.
- **A7.5 — Load baseline.** 50 sessions × 4 subscribers × continuous streaming on one
  replica: p95 append→deliver latency < 500ms, memory bounded (backpressure verified
  by throttling one consumer and observing `ResumeRequired` + successful resume).
  Recorded as a CI trend metric, alerting on >2x regression rather than a hard gate.
- **A7.6 — Retention roundtrip.** Archive a session, age its events past policy, run
  retention. Assert: rows tombstoned, archive object is seq-contiguous JSONL,
  export API serves it, `Subscribe from_seq=0` on the archived range returns the
  typed redirect error, and a restored session (import) replays identically.
- **A7.7 — Billing correctness.** Against stripe-mock (per-PR) and Stripe test mode
  (nightly): (a) an org with 3 members and 2 metered envs (one hosted, one byo)
  produces aggregation records exactly matching the `env_usage` ledger and seat count;
  re-running aggregation is idempotent (same Stripe quantities). (b) Checkout →
  webhook → `orgs.plan` updated; plan quota then permits a previously-denied
  hosted-EKS provision. (c) Past-due webhook → dunning: new provisions denied with a
  typed billing error, existing sessions still readable/subscribable; payment-restored
  webhook lifts the freeze. (d) A disputed interval is traceable: harness picks a
  ledger row and replays the session's env lifecycle events to reproduce its exact
  duration.

## Exit criteria

- **Capability-completeness audit passes:** compare this plan with actual proto RPCs, event variants, UI controls, desktop commands, and lifecycle states. Every implemented capability has capability-specific Go real-stack + Playwright + Rust desktop evidence and a truthful `e2e/coverage.json` entry whose files exist and run in required CI.
- Planned-but-unbuilt acceptance bullets remain explicitly incomplete; they are never silently omitted, renamed as complete, or represented by broader tests that do not assert them. `python3 scripts/verify-coverage.py` and all referenced suites pass.

- A7.1–A7.7 green (chaos/load on schedule, not per-PR; per-PR runs a 30s chaos smoke
  and stripe-mock billing tests).
- Dashboards/alerts deployed with the reference deployment manifests.
- Runbooks reviewed by walking each one against the chaos harness.
- Security review checklist complete (token storage, authz sweep, secret handling,
  TLS everywhere in reference deploy).
