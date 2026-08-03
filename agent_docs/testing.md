# Testing

The drift defense. Full strategy historically lived in `plan/index.md`; the
post-E1 client-evidence rule is iron rules 7/8 in `AGENTS.md`. Non-negotiable
principle: **tests exercise the real system through its real boundaries. No
mocks of our own components, ever.**

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
| Functional (first line) | `e2e/` | real Postgres + real `cored` + real `coreworker` child processes + generated clients |
| TS smoke | `clients/ts/smoke.test.ts` | same real stack, driven from `e2e/ts_smoke_test.go` |
| Provider conformance | `envprovider/conformance` | real Bezalel containers via the real provider |
| CLI | `cmd/core/cli/*_test.go` | real stack through the public API only |
| Dev-stack smoke | `scripts/dev-stack.sh smoke` | the documented one-command stack end to end |
| Gate mutation | `scripts/mutate-*.sh` | the real gates, deliberately broken then restored |
| Extraction fences | `scripts/check-extraction-fences.sh` | banned product terms must not reappear |

The only substituted component anywhere is the LLM vendor (`modelscript` —
a real HTTP/SSE server speaking the OpenAI API at the network boundary).
Fantasy, River, Postgres, cored, coreworker, and clients are always real.
There is no first-party web or desktop UI after E1; client evidence is the Go
functional suite + SDK smoke tests.

## Running

```sh
task test                       # unit + store + queue + provider conformance
task test:functional            # e2e/ — the acceptance suite
task test:all                   # everything
task cli:test                   # cmd/core against the real stack
task dev:smoke                  # one-command stack smoke, then full teardown
task verify:codegen             # generated output matches the protos
task verify:codegen:mutation    # prove the codegen gates fail on drift
task verify:coverage            # capability coverage matrix
task verify:coverage:mutation   # prove coverage rejects false evidence
go test ./e2e/ -run TestA02 -v  # one acceptance test
```

Requirements: docker running (testcontainers), `npx` + `npm ci` in
`clients/ts` once for the TS smoke. Local-only gates self-skip unless
enabled: set `CORE_DEV_STACK_TESTS=1` for the dev-stack smoke.

## Test infrastructure

- **`testkit/pgtest`** — one shared Postgres 17 container per test process;
  `NewDB(t)` / `NewPool(t)` create an isolated database per test (cheap,
  parallel-safe). Ryuk reaps the container on process exit.
- **`testkit/harness`** — `harness.Up(t)` returns a `*Stack`: migrated fresh
  DB, seeded identities/credential, modelscript, and `cored` + `coreworker`
  running as **real child processes** (binaries built once). It exposes
  KillWorker/StartWorker for crash tests, and `Logs()` returning everything
  cored and the workers wrote to stderr (used by the redaction sweep).
  Cleanup is automatic.
  Seeded fixtures: OrgA/alice (`harness.TokenAlice`), OrgB/bob
  (`harness.TokenBob`) — two orgs so tenant isolation is always testable.
- **`testkit/testclient`** — wraps the generated Connect client (the same
  artifact users get) with bearer auth and helpers:
  `AppendUserMessage`, `Subscribe`, `Subscription.Collect(t, n, timeout)`.
  If the public API can't do something, tests can't either — by design.

### Multi-replica harness

`harness.Up(t, harness.WithReplicas(2, 2))` starts two `cored` processes
behind a round-robin ingress plus two workers, so "worker 0 died and worker 1
finished the job" is a statement about identifiable processes. It exposes:

- `IngressURL()` / `IngressClient(token)` — load balanced across replicas;
- `ReplicaBaseURLs` / `ReplicaClient(i, token)` — pinned to one replica;
- `StartWorker()`, `RestartWorker(i)`, `KillWorkerAt(i)`, `WorkerCount()`;
- `RestartUltrad(i)`, `Health(baseURL)`;
- `QueueDepth(ctx, kinds...)` and `QueueDepthForRun(ctx, runID)`.

Queue-depth assertions must be scoped to the job kinds or the run a test
actually cares about. A parked parent's own step job is still running while it
commits its wait, and its children legitimately hold slots, so an unscoped
depth check is a flake waiting to happen.

### Constructing races instead of sleeping for them

`modelscript` selects a turn by **conversation depth** — the number of
assistant messages so far — among the turns whose matcher accepts the request.
Sticky turns only apply past the end of the matching set. Two features exist
specifically to make race tests deterministic:

- `Turn.Gate <-chan struct{}` blocks a scripted response until the test closes
  the channel, so a run can be held in a known state without sleeping.
- `Turn.Scenario` labels which independent scenario a turn belongs to. When a
  request matches turns from two different labelled scenarios, the server
  refuses rather than guessing: that situation means one scenario's prompt is a
  substring of another's, and silently picking one produces a wrong answer that
  looks like a product bug.

A race test that only passes with sleeps or a fixed ordering is not acceptance
evidence. Run the distributed suites repeatedly with process-kill timing
jitter before believing them.

## Capability-completeness gate

Every implemented public capability must be exercised through the real Go
functional suite (and, from E4, Go/TS SDK smoke tests). Follow
`agent_docs/cross_client_testing.md` where it still applies post-E1, and keep
`e2e/coverage.json` truthful. The matrix is Go-only after E1 — no web/desktop
columns.

A test counts only if it uses the real harness stack and asserts observable
behavior. Compile checks, control-presence checks, unasserted RPC calls,
nonexistent filenames, and tests that bypass application state are not
functional evidence. For stateful capabilities, cover replay/reconnect; for
tenant data, cover foreign-org denial; for distributed workflows, cover
failure/restart and concurrent observers where relevant.

Every PR adding/changing a proto RPC, event variant, state transition, or
provider/tool behavior must include:

- a Go real-stack functional assertion;
- a coverage-matrix row (or update) naming that test *and* the specific
  assertion strings that prove the capability.

## Conventions

- Acceptance tests are named `TestA<phase><n>_...` and map 1:1 to the
  acceptance criteria in the owning plan/inventory. Keep the mapping in
  comments.
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
  `golangci-lint`, extraction fences.
- Unit/store/queue job and functional job run separately; both required.
- Provider legs (k8s/nomad/tunnel/static) stay; web/desktop legs are gone.
