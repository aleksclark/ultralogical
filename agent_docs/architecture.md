# Architecture

Ultralogical is a durable-session platform for agentic work. Full design +
roadmap: [`plan/index.md`](../plan/index.md). This doc describes what exists
in the codebase today (Phase 0) and the load-bearing patterns you must
preserve.

## Components

```
clients (gen/go, clients/ts, testkit/testclient)
        │  ConnectRPC over HTTP (h1 + unencrypted h2)
        ▼
cmd/ultrad ── server/ (handlers, auth) ── server/eventbus (fan-out)
        │
        ▼
postgres/ (Store impl, goose migrations)      jobqueue/ (river | inproc)
        └──────────── one Postgres ────────────────┘
```

- **Root package `ultra`** — pure domain types + interfaces (`Store`,
  `OrgScope`, `EventStore`, ...). No I/O, no deps beyond stdlib. Ben Johnson
  layout: the root defines the domain, subpackages implement it.
- **`postgres/`** — the only Store implementation. Migrations are embedded
  (`postgres/migrations/*.sql`, goose format) and applied via
  `postgres.Migrate`.
- **`server/`** — Connect handlers, auth, proto↔domain conversion
  (`convert.go`). Transport-only: no business logic.
- **`server/eventbus`** — event fan-out (see below).
- **`jobqueue/`** — the queue seam; `river/` (prod) and `inproc/` (tests)
  implementations, `conformance/` shared suite.
- **`cmd/ultrad`** — the API server binary. Env-configured (`DATABASE_URL`,
  `ULTRA_ADDR`, `ULTRA_DEV_TOKENS`, `ULTRA_MIGRATE`).
- **`testkit/`** — `pgtest` (shared PG container, DB per test), `harness`
  (boots the real stack), `testclient` (wraps the generated client).
- **`e2e/`** — the functional API suite (acceptance tests A0.x).

## The event log (the core invariant)

Everything observable in a session is a typed event in `session_events` with
a **per-session, gapless, monotonic seq**:

- Seq assignment: `UPDATE sessions SET last_seq = last_seq + 1 ... RETURNING`
  inside the append transaction — serializes appends per session, guarantees
  gaplessness.
- The same transaction `pg_notify`s channel `session_events` with payload
  `<session_id>:<seq>`. The payload is **only a wakeup hint**: subscribers
  always read forward from their last delivered seq via `Range`.
- `eventbus.Bus.Subscribe` = catch-up read → LISTEN wakeups → 2s poll-tick
  fallback. Delivery is correct even if every notification is dropped.
- Streaming, multiplayer, history replay, and test assertions are all the
  same mechanism: `EventService.Subscribe(from_seq)`.
- Event payloads are stored as `(kind, protojson)`; `server/convert.go` maps
  the proto `EventPayload` oneof to and from that pair. New event variants =
  new oneof field + kind constant + convert cases (see agent_docs/codegen.md).

Subscribe streams send an initial **keepalive frame** (SubscribeResponse with
no event) to flush response headers — connect clients block until headers
arrive. Clients must skip event-less responses.

## Tenancy (multi-tenant from day 1)

- `orgs` / `users` / `org_members`; every session belongs to exactly one org.
- **`OrgScope` pattern**: all tenant data access goes through
  `store.Org(orgID)`, whose queries always carry the org id. Cross-tenant
  reads are structurally impossible, not merely checked. Never add an
  unscoped accessor for tenant data.
- The single directory method is `Store.SessionOrg(sessionID) → OrgID`;
  handlers resolve it, check membership, then use the scope.
- **No existence oracle**: missing rows and cross-tenant access both return
  `ultra.ErrNotFound`, mapped to the same `not_found` Connect error with the
  same message. Tests assert indistinguishability (A0.6).

## Auth

`server.Authenticator` seam. Current impl: static dev tokens
(`ULTRA_DEV_TOKENS="token=email,..."`) resolved to existing user rows. Unary
RPCs authenticate via interceptor; streaming RPCs authenticate inside the
handler (interceptors don't cover them). OIDC replaces the impl in Phase 7
behind the same interface.

## Queue seam

`jobqueue.Job/Enqueuer/Worker[J]/Queue` — no backend types leak through.
`EnqueueTx(ctx, tx, job)` is transactional: visible iff the surrounding pgx
tx commits. This is the mechanism for atomic entity-creation + first-job
(used heavily from Phase 1 on). The river backend wraps all jobs in a single
envelope kind and dispatches by inner kind. Any new backend must pass
`jobqueue/conformance` — that suite is the contract, not river's behavior.

## State snapshot (what is NOT built yet)

Agent runs, dev envs, flows, memory, presence, billing, the web UI — all in
later phases (`plan/phase_1.md`+). Don't invent stopgaps for them; follow the
plan docs.
