# Phase 0 — Skeleton & contracts

**Duration:** 1–2 weeks · **Depends on:** nothing · **Unblocks:** everything

## Goal

Stand up the repo, the schema-first API pipeline, the storage and queue seams, and the
test harness — so that every subsequent phase starts from a working, CI-gated vertical
slice. The deliverable is a running `ultrad` that can create sessions and stream events
to a generated client, with the drift gates already armed. **Tenancy is in the
foundation**: orgs, users, memberships, and org-scoped stores exist from the first
migration — retrofitting `org_id` later is the classic multi-tenant failure mode and we
refuse it.

## Scope

**In:**
- Repo layout (see [index §2.9](index.md#29-repo-layout)), Go module, task runner, CI.
- `proto/ultra/v1/` with `org.proto`, `session.proto`, `event.proto`; buf toolchain;
  committed codegen for Go and TypeScript.
- Domain types + `Store` interface (root package, Ben Johnson layout); `postgres/`
  implementation covering orgs, users, memberships, sessions, and events; goose
  migrations; pgx.
- Tenancy scaffolding: `orgs`, `users`, `org_members` tables; every store access goes
  through an org-scoped handle (`store.Org(orgID)`); dev auth maps static tokens →
  (user, org) pairs so isolation is testable from day 1.
- `OrgService.{CreateOrg,GetOrg,InviteMember,ListMembers}` (credential/provider RPCs
  land in Phases 1/2).
- `jobqueue` interface + `jobqueue/river` + `jobqueue/inproc` + conformance suite.
- `cmd/ultrad` serving `SessionService.{CreateSession,GetSession,ListSessions}` and
  `EventService.{Append,Subscribe}` via connect-go.
- Event fan-out: Postgres LISTEN/NOTIFY per session with poll-from-seq fallback, behind
  a `server/eventbus` interface.
- `testkit/harness` + `testkit/testclient`.
- CI: lint (golangci-lint), `buf lint`, `buf breaking`, codegen-diff gate, unit tests,
  functional suite.

**Out:** agent runs, envs, flows, real OIDC (static dev tokens map to seeded org/user
pairs), billing, web UI.

## Design details

### Proto & codegen

- `buf.gen.yaml` targets: `connect-go` (server + client), `connect-es` + `protobuf-es`
  (TS client). Rust deferred to Phase 8 but proto style must stay tonic-compatible
  (no Connect-only extensions).
- `SessionEvent` is a message with `session_id`, `seq`, `ts`, `actor`, and a `oneof
  payload` — starting variants: `UserMessage`, `Annotation`. Every later phase adds
  variants; `buf breaking` guarantees additive-only evolution.
- Every mutation response carries `event_seq` — the seq of the event the mutation
  produced. Clients await consistency by subscribing past that seq.

### Store

```go
// root package
type Store interface {
    Org(id OrgID) OrgScope             // all data access flows through an org scope
    Orgs() OrgStore                    // org lifecycle itself
    Tx(ctx context.Context, fn func(Store) error) error // nested-aware
}
type OrgScope interface {
    Sessions() SessionStore
    Events() EventStore
}
type EventStore interface {
    Append(ctx context.Context, sessionID ID, e Event) (seq int64, err error)
    Range(ctx context.Context, sessionID ID, fromSeq int64, limit int) ([]Event, error)
}
```

- The `OrgScope` pattern makes cross-tenant reads structurally impossible: every query
  it issues carries `org_id` (sessions directly; session-owned rows via join or
  denormalized column). There is no unscoped session accessor to misuse.

- Per-session seq via `UPDATE sessions SET last_seq = last_seq + 1 RETURNING last_seq`
  inside the append transaction — serializes appends per session (acceptable; a session
  is not a high-contention entity) and guarantees gapless monotonic seq.
- `Append` emits `NOTIFY session_events, '<session_id>:<seq>'` in the same transaction.

### Event fan-out (`server/eventbus`)

```go
type Bus interface {
    Subscribe(ctx context.Context, sessionID ID, fromSeq int64) (<-chan Event, error)
}
```

Implementation: catch-up read from `Range`, then LISTEN; on wake, read forward from the
last delivered seq (never trust the notify payload for content — it is only a wakeup).
This makes delivery correct even if notifications are dropped: a periodic poll tick
(e.g. 2s) covers missed notifies. The bus is interface-isolated so NATS can replace it
later without touching handlers.

### jobqueue

Interface as in [index §2.7](index.md#27-queue--river-behind-a-type-safe-seam).
Registration is generic — `Register[J Job](reg Registry, w Worker[J])` — so payload
decode is type-safe at the seam even though backends store JSON. The inproc impl must
honor transactional semantics: jobs enqueued via `EnqueueTx` become visible only when
the surrounding pgx tx commits (implemented with a commit hook), so tests exercise the
same visibility contract as river.

### testkit

- `harness.Up(t)`: starts postgres (testcontainers), runs migrations, boots `ultrad`
  and (from Phase 1) `worker` as real processes on random ports, returns a
  `*testclient.Client` wrapping the generated Go connect client.
- `testclient` helpers: `AwaitEvent(t, sessionID, matcher, timeout)`,
  `CollectUntil(...)`, `SubscribeFrom(...)` — all built on the public `Subscribe` RPC.

## Work breakdown

1. Repo scaffold: module, Taskfile, golangci config, CI pipeline skeleton.
2. Protos + buf config + codegen commit + diff gate.
3. Migrations + postgres store (orgs, users, members, sessions, events) + org-scope
   pattern + store tests against real Postgres.
4. eventbus (LISTEN/NOTIFY + poll fallback) + tests.
5. jobqueue interface, river impl, inproc impl, conformance suite.
6. ultrad: connect handlers, dev token→(user,org) auth, org membership check on every
   session RPC, graceful shutdown.
7. testkit harness (seeds two orgs + users by default) + testclient; wire functional
   suite into CI.
8. TS client smoke test (node) in CI.

## Acceptance tests

All run against the real stack via `testkit/harness` unless noted.

- **A0.1 — Client roundtrip, both languages.** `CreateSession` → `GetSession` returns
  identical data via the generated Go client. The same scenario runs under node using
  the generated TS client (a small vitest suite in `clients/ts`), hitting the same
  harness ultrad.
- **A0.2 — Append + fan-out.** Append a `UserMessage`; a subscriber from seq 0 receives
  it with `seq == 1` and correct payload; a second subscriber connected concurrently
  receives the identical event. The Append response's `event_seq` equals the received
  seq.
- **A0.3 — Resume contract.** Subscriber disconnects; two more events appended;
  resubscribe with `from_seq = 1` receives exactly events 2 and 3, in order, no
  duplicates, no gaps. Repeat with `from_seq = 0` and assert full replay.
- **A0.4 — jobqueue conformance (river + inproc).**
  - Transactional visibility: job enqueued in a tx is not worked before commit; rolled
    back tx never delivers.
  - At-least-once: worker panics on first delivery → job redelivered; test asserts a
    per-job attempt counter reaches 2.
  - Kind routing: two kinds registered, each worker sees only its own.
  - Backoff: failed job's next attempt respects the configured minimum delay.
- **A0.5 — Drift gates armed.** A CI job (or scripted test of the CI config) verifies:
  (a) modifying a proto field number fails `buf breaking`; (b) editing a generated file
  without regenerating fails the codegen-diff gate.
- **A0.6 — Tenant isolation smoke.** Harness seeds org A (user alice) and org B (user
  bob). Alice creates a session. Bob's `GetSession`, `ListSessions`, `Subscribe`, and
  `Append` against it are all denied with a typed error indistinguishable from
  not-found (no existence oracle); Alice's access works. Bob's `ListSessions` shows
  only org B sessions.

## Exit criteria

- CI green on: lint, buf lint/breaking, codegen diff, unit, store tests, jobqueue
  conformance (both impls), functional suite (A0.1–A0.3, A0.6), TS smoke.
- `task dev` boots the full local stack (postgres + ultrad) in one command.
- Functional suite wall time < 2 min at this phase.
