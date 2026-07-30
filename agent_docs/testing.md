# Testing

The drift defense. Full strategy: [`plan/index.md §3`](../plan/index.md).
Non-negotiable principle: **tests exercise the real system through its real
boundaries. No mocks of our own components, ever.**

This consciously replaces tenet 3 (shared mock subpackage) of
[`package_layout.md`](package_layout.md): the domain-interface connection
points that layout creates are exactly where we plug in *real*
implementations (postgres, river, inproc) validated by conformance suites —
not mocks. The isolation benefit is preserved; the drift risk of mocks is
not.

## Layers

| Layer | Where | What's real |
|---|---|---|
| Unit | alongside code | pure logic only |
| Store tests | `postgres/*_test.go` | real Postgres (testcontainers) |
| Queue conformance | `jobqueue/conformance` | real Postgres, real backend |
| Functional (first line) | `e2e/` | real Postgres + real `ultrad` + real `worker` child processes + generated clients |
| TS smoke | `clients/ts/smoke.test.ts` | same real stack, driven from `e2e/ts_smoke_test.go` |
| Web golden | `ui/web/e2e/` | real React SPA in Chromium + same real backend stack |

The only substituted component anywhere is the LLM vendor (`modelscript` —
a real HTTP/SSE server speaking the OpenAI API at the network boundary).
Fantasy, River, Postgres, ultrad, worker, clients, and UI are always real.

## Running

```sh
task test              # unit + store + queue (skips functional A0.x tests)
task test:functional   # e2e/ — the acceptance suite
task test:all          # everything
go test ./e2e/ -run TestA02 -v   # one acceptance test
```

Requirements: docker running (testcontainers), `npx` + `npm ci` in
`clients/ts` for the TS smoke (it self-skips when missing).

## Test infrastructure

- **`testkit/pgtest`** — one shared Postgres 17 container per test process;
  `NewDB(t)` / `NewPool(t)` create an isolated database per test (cheap,
  parallel-safe). Ryuk reaps the container on process exit.
- **`testkit/harness`** — `harness.Up(t)` returns a `*Stack`: migrated fresh
  DB, seeded identities/credential, modelscript, and `ultrad` + `worker`
  running as **real child processes** (binaries built once). It exposes
  KillWorker/StartWorker for crash tests. Cleanup is automatic.
  Seeded fixtures: OrgA/alice (`harness.TokenAlice`), OrgB/bob
  (`harness.TokenBob`) — two orgs so tenant isolation is always testable.
- **`testkit/testclient`** — wraps the generated Connect client (the same
  artifact users get) with bearer auth and helpers:
  `AppendUserMessage`, `Subscribe`, `Subscription.Collect(t, n, timeout)`.
  If the public API can't do something, tests can't either — by design.

## Conventions

- Acceptance tests are named `TestA<phase><n>_...` and map 1:1 to the
  acceptance criteria in `plan/phase_<n>.md`. Keep the mapping in comments.
- New seam implementation (queue backend, env provider, ...) ⇒ it must pass
  the existing conformance suite unmodified; never weaken a conformance
  suite to admit an implementation.
- Assert on the event log (`Subscribe from_seq`) rather than internal state
  wherever possible — that is the public contract.
- Tests use `t.Parallel()` and per-test databases; never share mutable
  fixtures across tests.
- `-count=1` always in CI (no cached test results).

## CI gates (.github/workflows/ci.yml)

- `buf lint`, `buf breaking` vs the PR base (schema evolution is
  additive-only), codegen diff (generated code must be committed in sync),
  `golangci-lint`.
- Unit/store/queue job and functional job run separately; both required.
