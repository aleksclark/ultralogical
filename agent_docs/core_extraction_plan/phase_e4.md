# Phase E4 — Consumer surface: API v1, SDKs, and ops hardening

**Objective:** Freeze the post-extraction shape as a coherent v1: reshaped
protos in `proto/core/v1`, a squashed schema baseline, embeddable **Go SDK**
and **TS SDK**, deployment story, and the redefined coverage gate. After
this phase the core is something primer and curri can actually link against
and deploy next to.

**Depends on:** E3.
**Duration guess:** 2 weeks.

---

## Scope

- Proto reshape (one planned break; additive-only resumes after).
- Migration squash to `00001_baseline.sql` (D7).
- Go SDK (`sdk/`) and TS SDK (grown from `clients/ts`).
- Single-binary + docker-compose deployment; config surface documented.
- Coverage gate v2; docs overhaul; version tagging.

## Work items

### T4.1 — Proto reshape → `proto/core/v1`

Services after reshape:

| Service | RPCs (summary) |
|---|---|
| `TenantService` | tenant CRUD, key create/revoke/list (admin-scope) |
| `CredentialService` | tenant inference credentials CRUD |
| `ProviderService` | register (probe), list, get w/ capabilities, deregister |
| `SessionService` | create (with labels), get, list (selectors), update labels, archive; memory get/set/list/delete |
| `RunService` | start run (prompt, ModelConfig, RunPolicy, Actor), get, list, cancel, answer (`ask_user` reply) |
| `ResourceService` | provision(kind, spec), get, list, terminate, restart, exec-preview (dev_env) |
| `EventService` | `Subscribe(session, fromSeq)` server stream; `Get(session, range)` |

Rules: every request carries Actor (interceptor-enforced); every list is
paginated; event payloads versioned (`{"v":1,...}` envelopes as today);
`buf breaking` gate enabled against v1 once merged.

### T4.2 — Schema squash

- Replace migrations 00001–00009 + E2/E3 interims with one
  `00001_baseline.sql` reflecting final names (tenants, api_keys, sessions
  + labels, events, runs, resources, provider_instances, credentials,
  periodic_prompts, river tables).
- Goose remains; additive-only discipline resumes (iron rule 4 analog).
- `task dev` and `pgtest` harness boot from the baseline; store conformance
  suite passes.

### T4.3 — Go SDK

- `sdk/` package: thin over generated ConnectRPC client + the ergonomics
  consumers actually need: auth transport (key header), Subscribe-with-
  reconnect (resume from last seen seq — the event log makes this exact),
  typed event decoding, run-await helper (`StartRun` + block until
  terminal/awaiting), label selector builder.
- The e2e `testkit/testclient` is **rebased onto the SDK** so the SDK is
  exercised by the entire functional suite, not by a smoke test alone.

### T4.4 — TS SDK

- Grow `clients/ts` into `@ultracore/client` (bufbuild connect-es
  generated + same ergonomics: auth, resume-from-seq subscribe, typed
  events). Node 22, ESM.
- Smoke suite expands: create session → start run against modelscript →
  stream deltas → resource lifecycle events → replay equality with Go
  client (byte-for-byte event payload comparison).

### T4.5 — Deployment + config

- `cored` (API) + `coreworker` (loop/resource jobs) + Postgres is the whole
  stack. Ship: single multi-binary docker image, `docker-compose.yml`
  reference deployment, systemd unit examples, and a Nomad job example
  (aleks' fleet is the first real deployment target).
- Config: env-only (`CORE_*`), documented exhaustively in `docs/deploy.md`
  with defaults; refuse-to-start on unknown `CORE_*` vars (config drift
  fence, mirroring provider unknown-field refusal).
- `/healthz` (liveness) + `/readyz` (pg + queue reachable) on both
  binaries; OTLP tracing of sessions/steps/tool-calls kept from the
  existing loop instrumentation and documented.

### T4.6 — Coverage gate v2 + docs

- `e2e/coverage.json` schema: capability → {go_functional, go_sdk, ts_sdk}
  columns (go_sdk usually satisfied via T4.3's testclient rebase).
  `verify:coverage` validates references and CI runs all three legs.
- Docs: rewritten `README.md` (what the core is / is not, quickstart),
  `docs/consumers.md` (how to embed: tenant setup, Actor conventions,
  label conventions, policy patterns), `docs/deploy.md`, updated
  `AGENTS.md`. The onboarding-kubernetes guide keeps its executable-guide
  test under new naming.
- Tag `v0.1.0` at phase close.

---

## Acceptance criteria

- **A4.1** Full suite green: `task build/lint/test/test:functional/cli:test/
  sdk:test`, `verify:codegen`, `verify:coverage` (v2 schema),
  `buf breaking` gate active, fences pass.
- **A4.2** Every public capability in coverage.json has go_functional +
  go_sdk + ts_sdk evidence; zero dangling references; the audit
  spot-checks 5 capabilities by deleting their tests and confirming
  `verify:coverage` fails.
- **A4.3** Fresh-clone proof: on a machine with only docker + go + node,
  `task dev` boots the full stack from the squashed baseline;
  `task dev:smoke` passes with leak checks.
- **A4.4** Reference deployment proof: docker-compose deployment on a clean
  host serves both SDKs' smoke suites over the network (not localhost
  assumptions); `/readyz` gates traffic correctly when pg is stopped.
- **A4.5** Subscribe-resume: SDK clients killed mid-stream and restarted
  resume from last seq with no gaps or duplicates (asserted against the
  gapless seq contract), in both Go and TS.
- **A4.6** Replay equality: Go SDK and TS SDK observe identical event
  sequences (payload-level comparison) for a scripted multi-resource,
  multi-run session.
- **A4.7** Unknown `CORE_*` env var refuses startup with a named error;
  documented config table matches `grep -r os.Getenv` reality (audited).
- **A4.8** `v0.1.0` tagged; `buf breaking` prevents a scratch breaking
  change (demonstrated once, reverted).

## Test coverage

| Behavior | Test | Tier |
|---|---|---|
| All capabilities ×3 legs | coverage.json matrix | functional + SDK |
| SDK reconnect/resume | new `e2e/sdk_resume_test.go` + TS suite | SDK |
| Cross-SDK replay equality | `e2e/replay_parity_test.go` | SDK |
| Fresh baseline boots + smoke | `task dev:smoke` in CI | stack |
| Compose deployment | CI job on clean runner | stack |
| Health/readiness semantics | stack test (pg stop → readyz fail) | stack |
| Config drift fence | unit test on config loader | unit |
| Proto compat | `buf breaking` in CI | CI |

## Exit audit

`phase_e4_audit.md`: A4.1–A4.8 with evidence; confirms the testclient→SDK
rebase left no direct generated-client usage in e2e (grep); records final
LOC and suite runtimes vs E0 baseline. Closing this phase unblocks both
consumer migrations.
