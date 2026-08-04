# Phase 9 inventory — Phase 4 bullet to bounded behavior to named test

Written before production changes, per Phase 9 required sequence step 1. Every
Phase 4 scope bullet and audit gap is decomposed into bounded observable
behaviors, the production entrypoint that exposes each, and the named test that
asserts it. Behavior that did not exist before Phase 9 is marked
**unimplemented before Phase 9** so the phase cannot close by renaming partial
work.

Legend: `go:` real-stack Go test, `cli:` `cmd/core` test against the real
stack, `web:` Playwright against the dark shadcn application, `gpui:` Rust test
driving the rendered GPUI window.

## Reconciliation: what Phase 4 actually shipped

| Phase 4 bullet | State entering Phase 9 |
|---|---|
| `flows` + `flow_invocations` tables | present, but invocations had no state, progress, terminal reason, or rendered record |
| `FlowService` Put/Get/List/Invoke | present; no Validate, no invocation reads, no cancel |
| Definition rendering with typed params | prompt-only, no env rendering, no param type checks |
| Version pinning | latest-wins `Put`; no explicit version, no overwrite rejection, no concurrent convergence |
| `FlowInvoked` event + provenance | event existed as an untyped annotation payload; envs carried no invocation link |
| Env declarations, agent-to-env wiring, readiness gating | **unimplemented before Phase 9** |
| Spawn topology and `agent_ref` catalog spawning | **unimplemented before Phase 9** |
| Rollback/cleanup on partial failure, retry idempotency | **unimplemented before Phase 9** |
| Structured path-addressed validation errors | single free-text string |
| `ultra` CLI | **unimplemented before Phase 9** |
| Flow UI in web and GPUI | **unimplemented before Phase 9** |
| Executable examples and docs | **unimplemented before Phase 9** |

## A9.1 — Versioning and validation

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| A valid definition is stored and assigned version 1, then 2 | `PutFlow` with `version=0` | `go: e2e TestA91_VersioningAndValidation` |
| An explicit version that already exists is rejected and the stored definition is unchanged | `PutFlow` explicit version → `already_exists` (**unimplemented before Phase 9**) | `go: e2e TestA91_VersioningAndValidation` |
| Concurrent auto-assign writes converge on distinct versions, overwriting nothing | per-`(org,name)` advisory lock in `postgres.flowStore.Put` (**unimplemented before Phase 9**) | `go: e2e TestA91_ConcurrentVersionConvergence` |
| Every invalid definition is rejected with stable typed field paths | `ultra.ValidateFlowDefinition` (**unimplemented before Phase 9**) | `go: e2e TestA91_ValidationWall` (table-driven over every code) |
| Validation is available without persisting | `ValidateFlow` RPC (**unimplemented before Phase 9**) | `go: e2e TestA91_ValidationWall`, `cli: TestFlowValidateReportsTypedErrors` |
| Nothing is persisted when validation fails | `ListFlows` after each rejection | `go: e2e TestA91_ValidationWall` |
| A cross-org flow name is indistinguishable from missing | `GetFlow` uniform `not found` | `go: e2e TestA91_CrossOrgFlowIsNotFound` |
| CLI renders the same typed field paths | `ultra flow validate --json` | `cli: TestFlowValidateReportsTypedErrors` |
| The dark shadcn application renders the same typed field paths | flow editor validation panel | `web: flows.spec.ts "rejects an invalid flow with typed field paths"` |
| The rendered GPUI window renders the same typed field paths | flow validation panel | `gpui: flow_e2e.rs renders_flow_validation_errors` |

## A9.2 — Deterministic provenance

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| An invocation pins `flow_id`, name, and version | `flow_invocations` row | `go: e2e TestA92_DeterministicProvenance` |
| Rendering is deterministic for identical params | `ultra.RenderFlow` (sorted traversal) | `go: TestRenderFlowIsDeterministic` (unit), `go: e2e TestA92_DeterministicProvenance` |
| The rendered prompt, grants, and env specs used by the invocation are persisted | `flow_invocations.rendered` (**unimplemented before Phase 9**) | `go: e2e TestA92_DeterministicProvenance` |
| A later version cannot alter the earlier invocation's runs, envs, or events | replay after `PutFlow` v2 | `go: e2e TestA92_DeterministicProvenance` |
| Every run created by the invocation carries the invocation id and its flow agent name | `agent_runs.flow_invocation_id`, `.flow_agent_name` (**unimplemented before Phase 9**) | `go: e2e TestA92_DeterministicProvenance` |
| Every environment created by the invocation carries the invocation id and its flow env name | `dev_envs.flow_invocation_id`, `.flow_env_name` (**unimplemented before Phase 9**) | `go: e2e TestA92_DeterministicProvenance` |
| Provenance survives replay from seq 0 | `Subscribe(from_seq=0)` | `go: e2e TestA92_DeterministicProvenance` |
| The web application shows flow/version/invocation provenance | invocation panel | `web: flows.spec.ts "invokes a flow and shows provenance and progress"` |
| The GPUI window shows flow/version/invocation provenance | invocation panel | `gpui: flow_e2e.rs renders_flow_invocation_progress` |

## A9.3 — Readiness gate

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| A flow declaring two environments creates exactly two envs on invoke | `flowwork.Service.advance` provisioning stage (**unimplemented before Phase 9**) | `go: e2e TestA93_ReadinessGate` |
| No run exists while any required environment is not ready | invocation stays `provisioning` | `go: e2e TestA93_ReadinessGate` |
| Runs are created only after all required environments pass health | readiness stage transition | `go: e2e TestA93_ReadinessGate` |
| Each run's grants name only its declared environments | rendered per-agent grants | `go: e2e TestA93_ReadinessGate` |
| An agent may not reach an undeclared environment | `Grants.AllowsEnv` denial | `go: e2e TestA93_ReadinessGate` |
| Delayed readiness is observable in the API as ordered progress | `flow_invocation_progress` events | `go: e2e TestA93_ReadinessGate` |
| Delayed readiness is observable in the web application | invocation progress list | `web: flows.spec.ts "invokes a flow and shows provenance and progress"` |
| Delayed readiness is observable in the GPUI window | invocation progress list | `gpui: flow_e2e.rs renders_flow_invocation_progress` |

## A9.4 — Topology

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Declared roots start first | `entry: true` agents | `go: e2e TestA94_Topology` |
| A dependent agent starts only after every agent it declares `after` is terminal | invocation running stage | `go: e2e TestA94_Topology` |
| A dependent agent's `parent_run_id` is its first declared dependency's run | run creation | `go: e2e TestA94_Topology` |
| Agents sharing a stage form one cohort with stable ordinals | deterministic cohort id + declaration order | `go: e2e TestA94_Topology` |
| A spawnable catalog agent can be spawned by name with clamped grants | `spawn_agent` `agent_ref` (**unimplemented before Phase 9**) | `go: e2e TestA94_AgentRefSpawn` |
| An `agent_ref` that is not spawnable is denied uniformly | catalog lookup denial | `go: e2e TestA94_AgentRefSpawn` |
| The invocation reaches a reproducible terminal result naming per-agent outcomes | `flow_invocations.terminal_reason` + `GetFlowInvocation` | `go: e2e TestA94_Topology` |
| Topology is rendered in the web application | invocation topology list | `web: flows.spec.ts "invokes a flow and shows provenance and progress"` |
| Topology is rendered in the GPUI window | invocation topology list | `gpui: flow_e2e.rs renders_flow_invocation_progress` |

## A9.5 — Failure convergence

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| One environment failing provisioning while another succeeds converges the invocation to `failed` | provisioning stage failure detection | `go: e2e TestA95_FailureConvergence` |
| No agent run is created | run count is zero | `go: e2e TestA95_FailureConvergence` |
| Environments owned by the invocation are terminated exactly once | cleanup stage scoped by `flow_invocation_id` | `go: e2e TestA95_FailureConvergence` |
| A session environment not owned by the invocation is untouched | ownership predicate | `go: e2e TestA95_FailureConvergence` |
| Redelivery of the advance job creates no duplicate envs or runs | unique `(flow_invocation_id, flow_env_name)` / `(org_id, spawn_key)` | `go: e2e TestA95_FailureConvergence` |
| Re-invoking the same flow after failure creates a new invocation with its own resources | fresh invocation id | `go: e2e TestA95_FailureConvergence` |
| A definition that becomes invalid after load converges to `failed` with a typed reason | validation-after-load in the advance worker | `go: e2e TestA95_ValidationAfterLoad` |
| An invocation that cannot progress converges on an outer deadline | invocation deadline in the advance worker | `go: e2e TestA95_InvocationDeadlineConverges` |

## A9.6 — Cancellation and replay

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Cancelling during provisioning terminates owned envs and starts no run | `CancelFlowInvocation` (**unimplemented before Phase 9**) | `go: e2e TestA96_CancelDuringProvisioning` |
| Cancelling during execution cancels the invocation's runs | cancel stage | `go: e2e TestA96_CancelDuringExecution` |
| Cancellation is idempotent | repeated `CancelFlowInvocation` | `go: e2e TestA96_CancelDuringExecution` |
| Replay from seq 0 reconstructs ordered progress with no gaps | `Subscribe(from_seq=0)` | both A9.6 tests |
| Run and environment terminals appear in replay | run/env lifecycle events | both A9.6 tests |
| The final invocation state is the same on replay as live | `GetFlowInvocation` | both A9.6 tests |
| The web application can cancel and recover the same state after reload | cancel control + reload | `web: flows.spec.ts "cancels an invocation and recovers state after reload"` |
| The GPUI window can cancel and recover the same state after reconnect | cancel control + replica switch | `gpui: flow_e2e.rs cancels_and_recovers_flow_invocation` |

## A9.7 — CLI parity

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| `ultra flow validate` reports typed errors and exits nonzero | `cmd/core` (**unimplemented before Phase 9**) | `cli: TestFlowValidateReportsTypedErrors` |
| `ultra flow put` stores a version and prints it as JSON | `PutFlow` | `cli: TestFlowPutGetListRoundTrip` |
| `ultra flow list` and `get` match API state | `ListFlows`, `GetFlow` | `cli: TestFlowPutGetListRoundTrip` |
| `ultra flow invoke --param k=v` invokes and prints the invocation id | `InvokeFlow` | `cli: TestFlowInvokeStatusAndCancel` |
| `ultra flow status` prints state, progress, runs, and envs | `GetFlowInvocation` | `cli: TestFlowInvokeStatusAndCancel` |
| `ultra flow cancel` converges the invocation | `CancelFlowInvocation` | `cli: TestFlowInvokeStatusAndCancel` |
| The CLI uses only the generated public client, never Postgres | `cmd/core` imports | `cli: TestCLIUsesOnlyPublicAPIs` |
| A typed failure exits nonzero with a machine-readable error | error rendering | `cli: TestFlowValidateReportsTypedErrors` |

## A9.8 — Application parity

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| The catalog lists org flows with their latest version | flow catalog panel | `web: flows.spec.ts "lists the flow catalog and selects a version"`, `gpui: flow_e2e.rs renders_flow_catalog_and_version_selection` |
| A specific version can be selected and its definition shown | `GetFlow(version)`, `ListFlowVersions` | same |
| Structured validation errors are shown and correctable | validation panel | `web: flows.spec.ts "rejects an invalid flow with typed field paths"`, `gpui: flow_e2e.rs renders_flow_validation_errors` |
| A parameterized invocation can be launched from the application | invoke form | `web: flows.spec.ts "invokes a flow and shows provenance and progress"`, `gpui: flow_e2e.rs renders_flow_invocation_progress` |
| Readiness, topology, and provenance are rendered | invocation panel | same |
| Linked runs and environments are reachable from the invocation | run/env ids in the panel | same |
| An invocation can be cancelled from the application | cancel control | `web: flows.spec.ts "cancels an invocation and recovers state after reload"`, `gpui: flow_e2e.rs cancels_and_recovers_flow_invocation` |
| Reconnecting rebuilds the same invocation state | reload / alternate replica | same |

## A9.9 — Executable documentation

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| The single-agent example validates and completes | `examples/flows/single-agent.json` | `go: e2e TestA99_ExampleFlows/single-agent` |
| The environment-backed example validates, provisions, and completes | `examples/flows/environment-backed.json` | `go: e2e TestA99_ExampleFlows/environment-backed` |
| The multi-agent example validates and completes its declared topology | `examples/flows/multi-agent.json` | `go: e2e TestA99_ExampleFlows/multi-agent` |
| Every example named in `docs/flows.md` exists and is executed | doc/example cross-check | `go: e2e TestA99_DocumentedExamplesAreExecuted` |
| The documented event sequence for an example matches the log | expected-sequence assertion | `go: e2e TestA99_ExampleFlows` |
