# Phase 12 — Production security, billing, operations, and queue-swap proof

**Duration:** 3–4 weeks · **Depends on:** Phase 11

## Goal

Make the completed product safe, operable, and sellable: real human authentication and exhaustive authorization, ledger-backed billing, a second production queue backend, bounded event delivery and retention, graceful deploys, observability, and chaos/load proof.

## Scope

**In:**

- Pluggable OIDC authentication, participant API tokens, session roles, and a proto-derived authorization policy covering every RPC and nested tenant identifier.
- Stripe-backed seat and environment billing with idempotent aggregation, checkout/portal/webhooks, plans, quotas, grace, and dunning.
- `jobqueue/pgq`, a PostgreSQL `FOR UPDATE SKIP LOCKED` backend passing conformance and the complete functional suite.
- Event-stream backpressure and client auto-resume, archive/export/import retention, graceful ultrad/worker drain, readiness, and connection budgets.
- Structured logs, dashboards, alerts, trace links, backup/restore, runbooks, load tests, and chaos tests.
- Complete admin, billing, operations, and recovery surfaces in dark shadcn and GPUI applications.

**Out:** multi-region, enterprise SSO/SCIM, purchase-order invoicing, and seat proration.

## Required implementation sequence

1. Generate the RPC authorization inventory from proto descriptors. The build must fail when an RPC or tenant-bearing field lacks explicit policy and test coverage.
2. Implement OIDC behind `ultra.Authenticator`; verify issuer, audience, signature, expiry, nonce/state where applicable, user mapping, and membership. Retain static tokens only for explicit development/harness configuration.
3. Enforce org and session roles uniformly for unary and streaming RPCs. Recursively mutate all resource identifiers in the cross-tenant sweep and preserve the missing/cross-tenant indistinguishability contract.
4. Implement billing from the authoritative usage ledger and membership records. Persist idempotency keys for aggregation, Stripe calls, and webhooks; tolerate duplicates, reordering, and Stripe outage without locking readable data.
5. Enforce plan quotas at transactional claim points so concurrent requests cannot overrun seats, environments, or hosted access.
6. Implement `jobqueue/pgq` solely through the queue seam with transactional enqueue, claim lease, heartbeat, visibility timeout, attempts, backoff, scheduled jobs, drain, and panic/process-death redelivery.
7. Run the entire functional and application suite with River and pgq. A small conformance-only test does not prove the swap.
8. Bound subscriber memory, return typed resume state, and implement duplicate-free auto-resume in Go, TypeScript/web, and Rust/GPUI clients.
9. Archive only eligible session ranges to real S3-compatible object storage in contiguous, checksummed objects. Export and import must preserve events and provenance; active sessions remain online.
10. Implement drain/readiness and rolling restart in the multi-replica harness. Account for every database pool, listener, queue, scheduler, and hook connection.
11. Build dashboards and alerts from emitted metrics/traces/logs; exercise runbooks through fault injection and backup/restore rehearsals.
12. Run per-PR smoke and scheduled sustained chaos/load. Publish reproducible configuration and artifacts; assert invariants, not brittle timings.
13. Independently audit every public RPC, billing transition, operational state, alert, and runbook. No production claim closes from a happy-path test alone.

## Acceptance tests

- **A12.1 — AuthN/AuthZ sweep.** For every RPC: unauthenticated, invalid OIDC, nonmember, observer/member/admin/owner, missing, and cross-tenant cases match policy. Nested IDs are fuzzed. Streaming RPCs obey the same rules. A descriptor mutation without policy fails CI.
- **A12.2 — Billing correctness.** Three seats and hosted/BYO usage aggregate exactly from authoritative records; reruns and duplicate webhooks are idempotent; checkout changes plan; concurrent quota claims cannot exceed limits; past due freezes new paid resources but leaves sessions/export readable; payment restoration lifts the freeze.
- **A12.3 — Billing traceability.** Every Stripe quantity is reproducible from ledger rows and session lifecycle events. A disputed interval can be exported with checksums and no secret leakage.
- **A12.4 — Queue swap.** River, inproc, and pgq pass conformance. The complete Go functional, Playwright, and GPUI real-stack suites pass with `ULTRA_QUEUE=pg`, including retries, scheduled work, hooks, flows, environments, and cohorts.
- **A12.5 — Backpressure and resume.** A deliberately slow subscriber remains memory-bounded, receives typed resume state, and all shipped clients reconnect to a complete gap-free, duplicate-free sequence while fast consumers remain unaffected.
- **A12.6 — Retention round trip.** Archive an aged archived session to S3-compatible storage, verify contiguous checksummed objects and tombstones, export/import it, and replay an equivalent event log. Active sessions and unauthorized orgs cannot access the object.
- **A12.7 — Zero-loss deploy.** Rolling-restart two ultrad and drain/restart workers while runs, waits, schedules, hooks, environments, and subscribers are active. Clients converge; jobs finish or redeliver; no event gaps, duplicate steps, or orphaned resources occur.
- **A12.8 — Chaos.** Under mixed concurrent workloads, repeatedly kill workers, restart ultrad, sever database connections, and kill provider resources. Assert terminal convergence, uniqueness, gaplessness, bounded retries, metering closure, and final leak-free reconcile.
- **A12.9 — Load and budgets.** The documented workload records p50/p95/p99 API and event lag, queue delay, connection count, memory, and error rate. Regression thresholds are based on a checked-in baseline and resource budget.
- **A12.10 — Operations and recovery.** Dashboards display induced faults, alerts fire and clear, trace/log links preserve correlation, backup/restore recovers the authoritative state, and each runbook is executed successfully against the harness.
- **A12.11 — Admin applications.** Web and GPUI complete login, membership/role management, billing/usage, checkout/portal return, quota/dunning errors, archive/export, operational health, and reconnect flows through shipped clients.

## Validation commands

```sh
task generate
task verify:codegen
task lint
task test
task test:functional
ULTRA_QUEUE=pg task test:functional
task web:test
ULTRA_QUEUE=pg task web:test
cargo test --manifest-path ui/desktop/Cargo.toml
ULTRA_QUEUE=pg cargo test --manifest-path ui/desktop/Cargo.toml
python3 scripts/verify-coverage.py
git diff --check
```

Per-PR CI runs short chaos, stripe-mock, object-storage, OIDC, and queue-matrix jobs. Sustained chaos/load, Stripe test mode, and backup/restore run on protected schedules.

## Exit criteria

- A12.1–A12.11 pass in required CI or explicitly scheduled jobs.
- Every RPC has generated policy coverage; every queue backend runs the full product suite; every billing side effect is idempotent and ledger-reproducible.
- Rolling deploy, backpressure, retention, backup/restore, dashboards, alerts, and runbooks are proven under injected failures.
- The independent production-readiness audit has no unowned critical or high finding.
