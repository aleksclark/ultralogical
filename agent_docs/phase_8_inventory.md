# Phase 8 inventory — Phase 3 bullet to bounded behavior to named test

Written before production changes, per Phase 8 required sequence step 1. Every
open Phase 3 audit bullet is decomposed into bounded observable behaviors, the
production entrypoint that exposes each, and the named test that asserts it.
Behavior with no implementation is marked **unimplemented** so the phase cannot
close by renaming partial work.

Legend: `go:` real-stack Go test, `web:` Playwright against the dark shadcn
application, `gpui:` Rust test driving the rendered GPUI window.

## A8.1 — Spawn durability and grants (Phase 3: spawn tree, privilege lattice)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| A parent spawns a child with strictly narrower persisted grants | `spawn_agent` native tool | `go: e2e TestA81_SpawnDurabilityAndGrants` |
| A grandchild narrows further; the lattice holds transitively | `spawn_agent` in the child | `go: e2e TestA81_SpawnDurabilityAndGrants` |
| A request for broader grants than the parent is denied | `Grants.SubsetOf` at dispatch | `go: e2e TestA81_SpawnDurabilityAndGrants` |
| Denied tools are absent from the child's discovered tool set | tool assembly in `loop.StepWorker` | `go: e2e TestA81_SpawnDurabilityAndGrants` |
| A forged denied native dispatch fails uniformly and emits `PermissionDenied` | native tool guards | `go: e2e TestA81_SpawnDurabilityAndGrants` |
| A forged denied MCP dispatch fails the same way, leaking no existence | `loop.EnvTools` dispatch guard (**hardened in Phase 8**) | `go: e2e TestA81_SpawnDurabilityAndGrants` |
| Spawn is idempotent under queue redelivery: one child, one first step | `spawn_agent` idempotency key (**unimplemented before Phase 8**) | `go: e2e TestA81_SpawnIdempotentRetry` |
| `max_children` is enforced under concurrent spawns | child-count check under lock (**hardened in Phase 8**) | `go: e2e TestA81_SpawnIdempotentRetry` |
| A child's final result is persisted and readable by the parent | `agent_runs.result` (**unimplemented before Phase 8**) | `go: e2e TestA82_WaitRaceMatrix` |

## A8.2 — Wait race matrix (Phase 3: `wait_for_agents`)

Each case must resolve exactly one wait, inject exactly one tool result whose
id equals the originating Fantasy tool-call id, and resume the parent at most
once. Exactly-once is enforced by database uniqueness and row locks, never by
in-memory flags.

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| A child that terminated *before* the wait row commits still resolves it | wait creation re-checks members in-transaction (**unimplemented before Phase 8**) | `go: e2e TestA82_WaitRaceMatrix/child_terminal_before_wait_commit` |
| A child terminating after the commit resolves it | `resolveChildWaits` | `go: e2e TestA82_WaitRaceMatrix/child_terminal_after_wait_commit` |
| Duplicate terminal delivery resolves once and resumes once | `run_waits.state` conditional update | `go: e2e TestA82_WaitRaceMatrix/duplicate_child_terminal_delivery` |
| Mixed completed/failed/cancelled children resolve with per-child state | wait result payload | `go: e2e TestA82_WaitRaceMatrix/mixed_terminal_states` |
| Timeout before the last child resolves the wait as timed out | `wait.timeout` scheduled job (**unimplemented before Phase 8**) | `go: e2e TestA82_WaitRaceMatrix/timeout_before_last_child` |
| The last child racing an imminent timeout still yields one resolution | same lock/state guard | `go: e2e TestA82_WaitRaceMatrix/timeout_races_last_child` |
| Parent cancellation resolves/abandons the wait without a later resume | cancel path closes open waits (**unimplemented before Phase 8**) | `go: e2e TestA82_WaitRaceMatrix/parent_cancelled` |
| Worker death mid-resolution redelivers and still resumes once | transactional resolution + redelivery | `go: e2e TestA82_WaitRaceMatrix/worker_death_during_resolution` |
| The injected message is a tool result correlated to the original call id | envelope tool-result injection (**unimplemented before Phase 8**: current code injects a plain user message) | asserted in every subtest |

## A8.3 — Cohort fan-out/fan-in (Phase 3: `run_agent_cohort`)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| One tool call launches N children concurrently | `run_agent_cohort` (**unimplemented before Phase 8**) | `go: e2e TestA83_CohortFanOutFanIn` |
| Children are real runs with `parent_run_id` set | shared spawn primitive | `go: e2e TestA83_CohortFanOutFanIn` |
| The parent parks with no queued step while children work | wait primitive (queue depth for `agent.step` is zero) | `go: e2e TestA83_CohortFanOutFanIn` |
| The aggregate result preserves declaration order | member ordinal | `go: e2e TestA83_CohortFanOutFanIn` |
| Per-child terminal state and result are exposed | wait result payload | `go: e2e TestA83_CohortFanOutFanIn` |
| One failed member follows the documented policy without stalling | documented cohort policy | `go: e2e TestA83_CohortFanOutFanIn/failed_member` |
| Resumption needs no model-side polling | server-side fan-in | asserted by model-call count |

## A8.4 — Cross-replica subscription (Phase 3: horizontal scale)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| `WithReplicas(2,2)` starts exactly two healthy cored and two workers | harness (**completed in Phase 8**) | `go: e2e TestA84_CrossReplicaSubscription` |
| Subscribe on replica A, append and start on replica B | `EventService.Subscribe`, `AgentService.StartRun` | `go: e2e TestA84_CrossReplicaSubscription` |
| Restarting a replica loses no events; resume-by-seq converges | event bus catch-up | `go: e2e TestA84_CrossReplicaSubscription` |
| Direct and load-balanced clients see identical gap-free, duplicate-free sequences | round-robin ingress (**unimplemented before Phase 8**) | `go: e2e TestA84_CrossReplicaSubscription` |

## A8.5 — Worker takeover (Phase 3: horizontal scale)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Four concurrent multi-step runs execute across two workers | queue distribution | `go: e2e TestA85_WorkerTakeover` |
| Killing worker 0 mid-step lets worker 1 finish the workload | indexed `KillWorker(0)` (**unimplemented before Phase 8**) | `go: e2e TestA85_WorkerTakeover` |
| `(run_id, step_index)` stays unique despite redelivery | `agent_run_steps` uniqueness | `go: e2e TestA85_WorkerTakeover` |
| Terminal states are correct and event sequences have no gaps | event log | `go: e2e TestA85_WorkerTakeover` |
| Redelivery is bounded, not unbounded retry | attempt accounting | `go: e2e TestA85_WorkerTakeover` |

## A8.6 — Memory and presence (Phase 3: memory caps, presence)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Concurrent writes to one key are atomic; no lost update | advisory-lock write path | `go: e2e TestA86_MemoryAndPresence` |
| The 201st key and an oversized value are rejected with typed errors | cap checks inside the lock | `go: e2e TestA86_MemoryAndPresence` |
| Dotted-namespace keys are accepted; malformed keys are rejected | key validation (**unimplemented before Phase 8**) | `go: e2e TestA86_MemoryAndPresence` |
| Reconnect replay reconstructs identical memory and presence state | event log + snapshot RPCs | `go: e2e TestA86_MemoryAndPresence` |
| Stale presence expires to idle as documented | presence reaper (**unwired before Phase 8**) | `go: e2e TestA86_MemoryAndPresence` |
| Cross-tenant memory and presence reads are indistinguishable from missing | org-scoped stores | `go: e2e TestA86_MemoryAndPresence` |

## A8.7 — Application run trees

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| The web app renders parent/child linkage as a run tree | web run-tree view (**unimplemented before Phase 8**) | `web: renders a spawned run tree with child lanes` |
| The web app filters the timeline to one run's lane | lane filter (**unimplemented before Phase 8**) | `web: filters the timeline to one run lane` |
| The web app shows awaiting/timeout/failure transitions for waits | run status surface | `web: shows wait and failure transitions` |
| The web app inspects session memory written by an agent | memory inspector | `web: inspects agent-written session memory` |
| The web app reconnects through another replica and converges | replica switch control (**unimplemented before Phase 8**) | `web: reconnects through a second replica` |
| The GPUI window renders the same run tree and lanes | GPUI run-tree view (**unimplemented before Phase 8**) | `gpui: renders_run_tree_and_lanes` |
| The GPUI window renders wait/timeout/failure transitions | GPUI status rows | `gpui: renders_wait_transitions` |
| The GPUI window inspects memory and reconnects through another replica | GPUI memory panel + reconnect action | `gpui: inspects_memory_and_reconnects` |

## A8.8 — Throughput baseline

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| The documented workload runs on two cored and two workers | harness replicas | `go: e2e TestA88_ThroughputBaseline` |
| Machine-readable latency, throughput, retry, and fan-out lag are emitted | baseline reporter (**unimplemented before Phase 8**) | `go: e2e TestA88_ThroughputBaseline` |
| Assertions are invariants plus a generous ceiling, not marketing numbers | test assertions | `go: e2e TestA88_ThroughputBaseline` |
| The recorded artifact names workload, hardware, replicas, and queue config | `agent_docs/throughput_baseline.md` | `go: e2e TestA88_ThroughputBaseline` |

## A8.9 — Security documentation

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Documented grant narrowing matches enforced behavior | `docs/security.md` (**unimplemented before Phase 8**) | `go: e2e TestA89_SecurityDocumentation` |
| Documented token scope matches minted environment tokens | env token minting | `go: e2e TestA89_SecurityDocumentation` |
| Denial visibility matches: uniform errors plus `PermissionDenied` events | dispatch guards | `go: e2e TestA89_SecurityDocumentation` |
| Integration credentials are not inherited by children | credential resolution | `go: e2e TestA89_SecurityDocumentation` |
| Docs claim nothing broader than the tests prove | executable examples extracted from the doc | `go: e2e TestA89_SecurityDocumentation` |

## Explicitly unimplemented before Phase 8

1. Wait results injected as a correlated tool result (today: a plain user message).
2. Wait timeout as a durable scheduled job.
3. Pre-commit child-terminal race handling at wait creation.
4. Parent-cancellation closing open waits.
5. `run_agent_cohort`.
6. Persisted child final result.
7. Spawn idempotency under redelivery, and locked `max_children` enforcement.
8. Round-robin ingress, indexed worker control, and kind-scoped queue depth in the harness.
9. Presence reaper wiring and documented expiry.
10. Memory key-namespace validation.
11. Run-tree, lane-filter, wait-status, and replica-reconnect surfaces in both clients.
12. Throughput baseline harness and recorded artifact.
13. `docs/security.md` with executable examples.

## Owned by later phases

Flows (Phase 9), remote providers (Phase 10), advanced loop features (Phase 11),
production chaos/load and billing (Phase 12), desktop distribution (Phase 13).
Nothing here claims those rows.
