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
| Provider conformance | `envprovider/conformance` | real Bezalel containers via the real provider |
| Web golden | `ui/web/e2e/` | real shadcn SPA in Chromium + same real backend stack |
| GPUI golden | `ui/desktop/tests/` | real GPUI window rendering + same real backend stack |
| Dev-stack smoke | `scripts/dev-stack.sh smoke` | the documented one-command stack end to end |
| Gate mutation | `scripts/mutate-*.sh` | the real gates, deliberately broken then restored |

The only substituted component anywhere is the LLM vendor (`modelscript` —
a real HTTP/SSE server speaking the OpenAI API at the network boundary).
Fantasy, River, Postgres, ultrad, worker, clients, and UI are always real.

## Running

```sh
task test                       # unit + store + queue + provider conformance
task test:functional            # e2e/ — the acceptance suite
task test:all                   # everything
task web:test                   # Playwright golden on the real stack
task desktop:test               # GPUI golden on the real stack
task dev:smoke                  # one-command stack smoke, then full teardown
task verify:codegen             # generated output matches the protos
task verify:codegen:mutation    # prove the codegen gates fail on drift
task verify:coverage            # capability coverage matrix
task verify:coverage:mutation   # prove coverage rejects false evidence
go test ./e2e/ -run TestA02 -v  # one acceptance test
```

Requirements: docker running (testcontainers), `npx` + `npm ci` in
`clients/ts` and `ui/web`, and a Rust toolchain for the GPUI suites.
Local-only gates self-skip unless enabled: set `ULTRA_WEB_TESTS=1` for
Playwright without CI browsers configured and `ULTRA_DEV_STACK_TESTS=1` for the
dev-stack smoke.

## Test infrastructure

- **`testkit/pgtest`** — one shared Postgres 17 container per test process;
  `NewDB(t)` / `NewPool(t)` create an isolated database per test (cheap,
  parallel-safe). Ryuk reaps the container on process exit.
- **`testkit/harness`** — `harness.Up(t)` returns a `*Stack`: migrated fresh
  DB, seeded identities/credential, modelscript, and `ultrad` + `worker`
  running as **real child processes** (binaries built once). It exposes
  KillWorker/StartWorker for crash tests, and `Logs()` returning everything
  ultrad and the workers wrote to stderr (used by the redaction sweep).
  Cleanup is automatic.
  Seeded fixtures: OrgA/alice (`harness.TokenAlice`), OrgB/bob
  (`harness.TokenBob`) — two orgs so tenant isolation is always testable.
- **`testkit/testclient`** — wraps the generated Connect client (the same
  artifact users get) with bearer auth and helpers:
  `AppendUserMessage`, `Subscribe`, `Subscription.Collect(t, n, timeout)`.
  If the public API can't do something, tests can't either — by design.

## Capability-completeness gate

Backend acceptance is necessary but insufficient. Every implemented public
capability must also be exercised through each supported first-party client.
Follow `agent_docs/cross_client_testing.md` and keep `e2e/coverage.json`
truthful.

A test counts only if it uses the real harness stack and asserts observable
behavior. Compile checks, control-presence checks, unasserted RPC calls,
nonexistent filenames, and tests that bypass application state are not
functional evidence. For stateful capabilities, cover replay/reconnect; for
tenant data, cover foreign-org denial; for distributed workflows, cover
failure/restart and concurrent observers where relevant.

Every PR adding/changing a proto RPC, event variant, UI control, desktop
command, state transition, provider/tool, or billing behavior must include:

- a Go real-stack functional assertion;
- a Playwright scenario using the web application;
- a GPUI scenario that opens the real desktop window and asserts on the frame it
  rendered (`await_rendered`/`debug_bounds`), not on a headless state core;
- a coverage-matrix row (or update) naming those tests *and* the specific
  assertion strings that prove the capability.

If any client surface is not yet implemented, the capability is not complete
and the phase exit criterion remains open.

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
- Gates are themselves tested. `scripts/mutate-codegen-gate.sh` and
  `scripts/mutate-coverage-gate.sh` deliberately break each gate, assert it
  fails for the intended reason, and restore the tree; a gate nobody has seen
  fail is not a gate.

## CI gates (.github/workflows/ci.yml)

- `buf lint`, `buf breaking` vs the PR base (schema evolution is
  additive-only), codegen diff (generated code must be committed in sync),
  `golangci-lint`.
- Unit/store/queue job and functional job run separately; both required.
