# Architecture

ultracore is a durable-session substrate for agentic work. This doc describes
the post-E1 implementation snapshot and load-bearing patterns. Extraction
roadmap: [`core_extraction_plan/index.md`](core_extraction_plan/index.md).

Package organization follows the standard layout in
[`package_layout.md`](package_layout.md): root package = domain types,
subpackages grouped by dependency, main packages wire everything. (Tenet 3,
shared mocks, is deliberately replaced by conformance suites + real-backend
tests — see agent_docs/testing.md.)

## Components

```
clients (gen/go, clients/ts, testkit/testclient)
        │  ConnectRPC over HTTP (h1 + unencrypted h2)
        ▼
cmd/cored ──── http/ (transport adapter: handlers, auth, conversion)
        │                 │ transactional enqueue
        ▼                 ▼
core (domain)       jobqueue (river) ◄── cmd/coreworker (N replicas)
        ▲                                    │ one fantasy step/job
        │ implements                         ▼
postgres/ (Store, EventBus, migrations)   LLM provider (org BYO creds)
        └──────────────── one Postgres ──────────────┘
```

- **Root package `core`** (import alias `uc`) — the domain: types (`Org`,
  `User`, `Session`, `Event`, `Grants`, memory, waits) and interfaces
  (`Store`, `OrgScope`, `EventStore`, `EventBus`, `Authenticator`), plus
  domain-only logic (`DevTokenAuthenticator` — no external deps). The root
  package depends on nothing else in the app. Source files may still use
  historical names (`ultra.go`, `multiplayer.go`); the package name is
  `core`.
- **`postgres/`** — everything that depends on Postgres/pgx: the only `Store`
  implementation, the `EventBus` implementation (LISTEN/NOTIFY), and embedded
  goose migrations (`postgres/migrations/*.sql`, applied via
  `postgres.Migrate`). Historical product tables remain until the E4 squash.
- **`http/`** — the transport adapter between the domain and HTTP/ConnectRPC.
  All Connect/net-http code is isolated here: handlers, auth interceptor,
  proto↔domain conversion (`convert.go`). Transport-only: no business logic.
  Handlers depend on domain interfaces (`core.Store`, `core.EventBus`,
  `core.Authenticator`), never on `postgres` types.
- **`jobqueue/`** — the queue seam (interface package, like stdlib `io`);
  `river/` (prod) and `inproc/` (tests) implementations grouped by
  dependency, `conformance/` shared suite.
- **`loop/`** — fantasy-based durable agent loop: versioned registry, history
  envelopes, BYO model resolution, native tools, spawn/wait/cohort, flat
  tool allowlist at dispatch, and `StepWorker` (one queue job = one model
  step).
- **`secrets/`** — AES-GCM credential encryption + process-wide log/error
  redaction. Workers decrypt at point of use; secret values never reach
  events, histories, logs, or RPC responses.
- **`cmd/cored`** — main package: wires postgres + http + enqueue-only river
  client together. Requires `DATABASE_URL`, `CORE_DEV_TOKENS`, and
  `CORE_MASTER_KEY`.
- **`cmd/coreworker`** — stateless worker: river + `loop.StepWorker`; N
  replicas may work any run. Requires `DATABASE_URL` + `CORE_MASTER_KEY`.
- **`cmd/core`** — public-API CLI only (no store/queue shortcuts).
- **`testkit/`** — `pgtest`, `harness` (real cored + worker processes),
  `testclient`, and `modelscript` (scripted OpenAI server, the only fake).
- **`e2e/`** — real-stack functional API suite spanning implemented acceptance
  capabilities. Client evidence is this suite plus the TS smoke test; there
  is no first-party web or desktop UI.

## Clients

- **`clients/ts/`** — TypeScript client seed (generated protos + thin wrapper).
  Grows into the TS SDK in E4. Smoke: `e2e/ts_smoke_test.go`.
- **`gen/go/`** — committed Go codegen (server handlers + client stubs) under
  `core/v1`. Regenerate with `task generate`; CI diffs it.
- **`testkit/testclient`** — Go test-facing wrapper over the generated client.

Consumers bring their own UI. First-party `ui/` and `clients/rust` were
deleted in E1.

## The event log (the core invariant)

Everything observable in a session is a typed event in `session_events` with
a **per-session, gapless, monotonic seq**:

- Seq assignment: `UPDATE sessions SET last_seq = last_seq + 1 ... RETURNING`
  inside the append transaction — serializes appends per session, guarantees
  gaplessness.
- The same transaction `pg_notify`s channel `session_events` with payload
  `<session_id>:<seq>`. The payload is **only a wakeup hint**: subscribers
  always read forward from their last delivered seq via `Range`.
- `eventbus`: the domain interface is `core.EventBus`; the Postgres
  implementation (`postgres.EventBus`) = catch-up read → LISTEN wakeups →
  2s poll-tick fallback. Delivery is correct even if every notification is
  dropped.
- Streaming, history replay, and test assertions are all the same mechanism:
  `EventService.Subscribe(from_seq)`.
- Event payloads are stored as `(kind, protojson)`; `http/convert.go` maps
  the proto `EventPayload` oneof to and from that pair. New event variants =
  new oneof field + kind constant + convert cases (see agent_docs/codegen.md).

Subscribe streams send an initial **keepalive frame** (SubscribeResponse with
no event) to flush response headers — connect clients block until headers
arrive. Clients must skip event-less responses.

## Tenancy (multi-tenant from day 1)

- `orgs` / `users` / `org_members`; every session belongs to exactly one org.
  E3 renames Org→Tenant and replaces human identity with tenant API keys.
- **`OrgScope` pattern**: all tenant data access goes through
  `store.Org(orgID)`, whose queries always carry the org id. Cross-tenant
  reads are structurally impossible, not merely checked. Never add an
  unscoped accessor for tenant data.
- The single directory method is `Store.SessionOrg(sessionID) → OrgID`;
  handlers resolve it, check membership, then use the scope.
- **No existence oracle**: missing rows and cross-tenant access both return
  `core.ErrNotFound`, mapped to the same `not_found` Connect error with the
  same message. Tests assert indistinguishability (A0.6).

## Auth

`core.Authenticator` seam (root package). Current impl:
`core.DevTokenAuthenticator` — static dev tokens
(`CORE_DEV_TOKENS="token=email,..."`) resolved to existing user rows. The
http package extracts bearer tokens: unary RPCs authenticate via
interceptor; streaming RPCs authenticate inside the handler (interceptors
don't cover them). E3 replaces this with tenant API keys behind the same
interface.

## Queue seam

`jobqueue.Job/Enqueuer/Worker[J]/Queue` — no backend types leak through.
`EnqueueTx(ctx, tx, job)` is transactional: visible iff the surrounding pgx
tx commits. This is the mechanism for atomic entity-creation + first-job.
The river backend wraps all jobs in a single envelope kind and dispatches by
inner kind. Any new backend must pass `jobqueue/conformance` — that suite is
the contract, not river's behavior.

## What left in E1

Product surface deleted: flows/flowdef, billing/metering/`Org.Plan`, hosted
EKS isolation, human presence/participants, grants lattice (replaced by flat
`Tools []string` allowlist), first-party web SPA and GPUI desktop. Surviving
substrate: sessions, event log, agent loop, spawn/wait/cohort, session
memory, periodic prompts, five provider adapters, credentials, CLI.
