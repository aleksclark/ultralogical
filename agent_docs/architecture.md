# Architecture

Ultralogical is a durable-session platform for agentic work. Full design +
roadmap: [`plan/index.md`](../plan/index.md). This doc describes what exists
in the codebase today (Phase 2) and the load-bearing patterns you must
preserve.

Package organization follows the standard layout in
[`package_layout.md`](package_layout.md): root package = domain types,
subpackages grouped by dependency, main packages wire everything. (Tenet 3,
shared mocks, is deliberately replaced by conformance suites + real-backend
tests — see agent_docs/testing.md.)

## Components

```
clients (gen/go, clients/ts, testkit/testclient)   ui/web (React)
        │  ConnectRPC over HTTP (h1 + unencrypted h2)
        ▼
cmd/ultrad ──── http/ (transport adapter: handlers, auth, conversion)
        │                 │ transactional enqueue
        ▼                 ▼
ultra (domain)      jobqueue (river) ◄── cmd/worker (N replicas)
        ▲                                    │ one fantasy step/job
        │ implements                         ▼
postgres/ (Store, EventBus, migrations)   LLM provider (org BYO creds)
        └──────────────── one Postgres ──────────────┘
```

- **Root package `ultra`** — the domain: types (`Org`, `User`, `Session`,
  `Event`) and interfaces (`Store`, `OrgScope`, `EventStore`, `EventBus`,
  `Authenticator`), plus domain-only logic (`DevTokenAuthenticator` — no
  external deps). The root package depends on nothing else in the app.
- **`postgres/`** — everything that depends on Postgres/pgx: the only `Store`
  implementation, the `EventBus` implementation (LISTEN/NOTIFY), and embedded
  goose migrations (`postgres/migrations/*.sql`, applied via
  `postgres.Migrate`).
- **`http/`** — the transport adapter between the domain and HTTP/ConnectRPC.
  All Connect/net-http code is isolated here: handlers, auth interceptor,
  proto↔domain conversion (`convert.go`). Transport-only: no business logic.
  Handlers depend on domain interfaces (`ultra.Store`, `ultra.EventBus`,
  `ultra.Authenticator`), never on `postgres` types.
- **`jobqueue/`** — the queue seam (interface package, like stdlib `io`);
  `river/` (prod) and `inproc/` (tests) implementations grouped by
  dependency, `conformance/` shared suite.
- **`loop/`** — fantasy-based durable agent loop: versioned registry, history
  envelopes, BYO model resolution, native `ask_user` / `post_event` tools,
  and `StepWorker` (one queue job = one model step).
- **`secrets/`** — AES-GCM credential encryption + process-wide log/error
  redaction. Workers decrypt at point of use; secret values never reach
  events, histories, logs, or RPC responses.
- **`cmd/ultrad`** — main package: wires postgres + http + enqueue-only river
  client together. Requires `DATABASE_URL`, `ULTRA_DEV_TOKENS`, and
  `ULTRA_MASTER_KEY`.
- **`cmd/worker`** — stateless worker: river + `loop.StepWorker`; N replicas
  may work any run. Requires `DATABASE_URL` + `ULTRA_MASTER_KEY`.
- **`testkit/`** — `pgtest`, `harness` (real ultrad + worker processes),
  `testclient`, and `modelscript` (scripted OpenAI server, the only fake).
- **`e2e/`** — functional API/UI suite (acceptance tests A0.x + A1.x).

## Clients & UIs

Two distinct trees, both consumers of the same protos:

- **`clients/<lang>/`** — client *libraries* per language. Generated code +
  a thin ergonomic wrapper; published artifacts. Today: `clients/ts`
  (protobuf-es under `clients/ts/src/gen`, smoke test). Phase 8 adds
  `clients/rust`. The Go client is the generated code in `gen/go` (shared
  with the server); `testkit/testclient` is its test-facing wrapper.
- **`ui/<app>/`** — UI *applications*, each consuming a client library and
  owning its golden functional suite: `ui/web` (React + Vite + Tailwind,
  Playwright golden), `ui/gpui` (rust native, Phase 8). UIs never reach around the
  client API.
- **`gen/go/`** — committed Go codegen (server handlers + client stubs).
  Regenerate with `task generate`; CI diffs it.

## The event log (the core invariant)

Everything observable in a session is a typed event in `session_events` with
a **per-session, gapless, monotonic seq**:

- Seq assignment: `UPDATE sessions SET last_seq = last_seq + 1 ... RETURNING`
  inside the append transaction — serializes appends per session, guarantees
  gaplessness.
- The same transaction `pg_notify`s channel `session_events` with payload
  `<session_id>:<seq>`. The payload is **only a wakeup hint**: subscribers
  always read forward from their last delivered seq via `Range`.
- `eventbus`: the domain interface is `ultra.EventBus`; the Postgres
  implementation (`postgres.EventBus`) = catch-up read → LISTEN wakeups →
  2s poll-tick fallback. Delivery is correct even if every notification is
  dropped.
- Streaming, multiplayer, history replay, and test assertions are all the
  same mechanism: `EventService.Subscribe(from_seq)`.
- Event payloads are stored as `(kind, protojson)`; `http/convert.go` maps
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

`ultra.Authenticator` seam (root package). Current impl:
`ultra.DevTokenAuthenticator` — static dev tokens
(`ULTRA_DEV_TOKENS="token=email,..."`) resolved to existing user rows. The
http package extracts bearer tokens: unary RPCs authenticate via
interceptor; streaming RPCs authenticate inside the handler (interceptors
don't cover them). OIDC replaces the impl in Phase 7 behind the same
interface.

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
