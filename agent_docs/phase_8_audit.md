# Phase 8 independent audit

Performed after implementation, from the migration files, proto descriptors,
wait/run state transitions, harness capabilities, client test bodies, CI
configuration, and the Phase 3 audit rows — not from the implementation
narrative. Each row states the bounded behavior, the evidence that proves it,
and its verdict.

Evidence must be a named test that runs in required CI and asserts the
behavior. "Closed" means the success path, the material failure path, the
durability/replay path, and the tenancy path are all asserted where the
capability has them. A row that only compiles, only exists, or is only
plausible is open.

## A8.1 — Spawn durability and grants

| Behavior | Evidence | Verdict |
|---|---|---|
| A child's persisted grants are a strict subset of its parent's | `TestA81_SpawnDurabilityAndGrants` (reads the stored run, not the request) | closed |
| The lattice holds transitively through a grandchild | `TestA89_SecurityDocumentation/grandchild_cannot_escalate` (also asserts only the compliant grandchild exists) | closed |
| A request for wider authority than the parent holds creates no run | same subtest, via the grandchild count | closed |
| A human cannot widen a run past root grants | `TestA89_SecurityDocumentation/narrowing_only_at_start_run` (requires `PermissionDenied`, not silent clamping) | closed |
| Denied tools are absent from discovery | `TestA81_EnvironmentGrantEnforcement` | closed |
| A forged native dispatch is refused uniformly and emits `PermissionDenied` | `TestA89_SecurityDocumentation/denials_are_uniform_and_opaque` and `/denial_emits_an_audit_event` | closed |
| A forged MCP dispatch is refused the same way against a real container | `TestA81_EnvironmentGrantEnforcement` (denies `write` on a real Bezalel env) | closed |
| A denial performs no side effect | `TestA81_EnvironmentGrantEnforcement` (the file does not exist in either environment) and `TestA89_.../denied_side_effect_never_happens` | closed |
| Denials disclose neither the tool inventory nor resource existence | `denials_are_uniform_and_opaque` (rejects `"Available tools"`, `"not found"`, and any resource id in the message) | closed |
| Spawn is idempotent under redelivery: one child, one first step | `TestA81_SpawnIdempotentRetry` | closed |
| `max_children` holds under concurrent spawns in one step | `TestA81_SpawnIdempotentRetry` (parent row lock is what makes the count correct) | closed |
| A parent may not wait on a run it did not parent | `TestA81_WaitAuthorityIsScopedToOwnChildren` | closed |

Two defects were found and fixed while writing the A8.9 evidence, both of
which would have made the rows above false:

1. **Built-in tools bypassed the lattice.** `ask_user`, `post_event`, and the
   four session-memory tools were registered unconditionally, so a child
   restricted to a single narrow task could still question the human and read
   or overwrite shared session memory. Now gated like every other capability
   (`loop/step.go`, `loop/memorytools.go`).
2. **An empty grant record was silently upgraded to root.** `postgres.runStore.Create`
   treated "no tools, no envs, no spawn" as unspecified and substituted
   `RootGrants()`. That inverted the most restrictive case we support: a child
   spawned with an explicitly empty tool list received full authority. All
   three call sites already set grants explicitly, so the fallback was pure
   escalation risk and is removed.

## A8.2 — Wait race matrix

Every subtest asserts the same three invariants through
`assertSingleCorrelatedResult`: exactly one wait reached the expected terminal
state, exactly one tool result was injected, and its `ToolCallID` equals the
originating `wait_for_agents` call.

| Race | Evidence | Verdict |
|---|---|---|
| Child terminal *before* the wait row commits | `TestA82_WaitRaceMatrix/child_terminal_before_wait_commit` | closed |
| Child terminal after the commit | `/child_terminal_after_wait_commit` | closed |
| Duplicate terminal delivery | `/duplicate_child_terminal_delivery` | closed |
| Mixed completed/failed/cancelled members | `/mixed_terminal_states` | closed |
| Timeout before the last child | `/timeout_before_last_child` | closed |
| Timeout racing the last child | `/timeout_races_last_child` | closed |
| Parent cancellation | `/parent_cancelled` | closed |
| Worker death mid-resolution | `/worker_death_during_resolution` | closed |

The exit criterion demands database enforcement rather than in-memory flags.
Verified against the schema and queries, not the prose:

| Guarantee | Enforcement | Verdict |
|---|---|---|
| A wait resolves once | `Close()` carries `WHERE state='open'`; a second caller affects zero rows and returns false | closed |
| A parent resumes at most once | `MarkResumed()` carries `WHERE resumed_at IS NULL` | closed |
| A wait times out once | `ClaimDue()` performs the `open → timed_out` transition *as* the claim, so two sweepers cannot both expire one wait | closed |
| A parent holds at most one open wait | partial unique index `run_waits_one_open_per_parent_idx` (migration 00007) | closed |
| A wait cannot exist unexpired | the timeout job is enqueued in the same transaction as the wait row | closed |

Races are constructed rather than slept for: `modelscript.Turn.Gate` holds a
scripted response open until the test closes it. The suite was run three times
to check for timing sensitivity.

## A8.3 — Cohort fan-out/fan-in

| Behavior | Evidence | Verdict |
|---|---|---|
| One tool call launches N real children with `parent_run_id` set | `TestA83_CohortFanOutFanIn` | closed |
| Members share a cohort id and carry declaration order | same test (asserts `CohortID` equality and `CohortOrdinal == i`) | closed |
| The aggregate result preserves declaration order | same test (asserts outcome member ordinals) | closed |
| Per-child terminal state and result are exposed, not merged | same test (requires each member's own text in the result) | closed |
| A failed member is recorded without stalling its siblings | `TestA83_CohortFailedMember` | closed |
| The parent parks with no queued step of its own | `TestA83_CohortParksWithoutQueuedParentStep` | closed |
| Resumption needs no model-side polling | same test: the parent's queue depth is zero and *stays* zero while the child works, so nothing is polling | closed |

The parking test is the substantive one. It also asserts the child holds a
runnable slot, so "the parent is parked" cannot pass vacuously by the parent
being parked on nothing.

## A8.4 — Cross-replica subscription

| Behavior | Evidence | Verdict |
|---|---|---|
| `WithReplicas(2,2)` starts exactly two healthy ultrad and two workers | `TestA84_CrossReplicaSubscription` asserts counts *and* health, not just configuration | closed |
| Subscribe on replica A, append and start on replica B | same test | closed |
| Restarting replica A loses nothing; resume-by-seq rebuilds it | same test (`RestartUltrad(0)`, then a fresh client from seq 0) | closed |
| Direct and load-balanced views are identical | same test compares full `(seq, kind, payload)` keys, not counts | closed |
| Sequences are gap-free and duplicate-free | `assertGapFree` on all three views | closed |
| Partial resume returns exactly the tail | same test asserts the first delivered seq is `from+1` | closed |

## A8.5 — Worker takeover

| Behavior | Evidence | Verdict |
|---|---|---|
| Four concurrent multi-step runs across two workers | `TestA85_WorkerTakeover` | closed |
| Worker 0 is killed while the workload is genuinely in flight | same test waits for recorded steps before killing, rather than killing on a timer | closed |
| Worker 1 finishes everything | same test awaits `completed` for all four | closed |
| `(run_id, step_index)` stays unique despite redelivery | same test | closed |
| Redelivery is bounded | same test rejects any step reaching attempt > 5 | closed |
| The event log has no gaps | `assertGapFree` over the full session log | closed |
| The queue drains rather than retrying forever | same test polls `QueueDepth(ctx, "agent.step")` to zero | closed |

## A8.6 — Memory and presence

| Behavior | Evidence | Verdict |
|---|---|---|
| Concurrent writes to one key are atomic; no torn value | `TestA86_MemoryAndPresence/concurrent_writes_are_atomic` (ten racing writers; the final value must be exactly one writer's) | closed |
| Every accepted write produced exactly one event | same subtest | closed |
| Key namespace rules are enforced | `/key_namespace_is_enforced` | closed |
| Key-count and value-size caps are enforced | `/caps_are_enforced` | closed |
| Agent-written memory survives run boundaries and is visible to other agents | `/agent_written_memory_survives_run_boundaries` | closed |
| Stale presence expires to idle as documented | `/presence_expires_and_replays_identically` (real reaper job, `ULTRA_PRESENCE_AFTER=2s` in the harness) | closed |
| Reconnect replay reconstructs identical memory and presence | same subtest | closed |
| Cross-tenant reads are indistinguishable from missing | `/cross_tenant_access_is_indistinguishable`, reinforced by `TestA89_.../cross_tenant_reads_are_indistinguishable_from_missing` which compares code *and* message across three RPCs | closed |

## A8.7 — Application run trees

Web evidence is `ui/web/e2e/run-tree.spec.ts` driving the production build;
GPUI evidence is `ui/desktop/tests/run_tree_e2e.rs` asserting on
`debug_bounds` output from the real window. No row here is backed by a
direct-RPC or reducer-only test.

| Behavior | Web | GPUI | Verdict |
|---|---|---|---|
| Parent/child linkage renders as a tree | `renders a spawned run tree with child lanes` (asserts `data-parent-run-id` equals the root's id) | `renders_run_tree_and_lanes` (asserts each painted child points at the parent) | closed |
| Cohort membership is visible | `cohort-marker` count | `cohort_id` non-empty per painted member | closed |
| The timeline filters to one agent's lane | `filters the timeline to one run lane` (every visible row belongs to the selected run; clearing restores) | `filters_timeline_to_one_lane` (lane row count painted, strict narrowing asserted) | closed |
| Wait state transitions are visible | `shows wait and failure transitions` (open → `timed_out`, parent still completes) | `renders_wait_transitions` (requires the open state was painted *before* the timeout, so a wait cannot appear pre-expired) | closed |
| Agent-written memory is inspectable | `inspects agent-written session memory` | `inspects_memory_and_reconnects` (`memory-count:0` then `memory-count:1`) | closed |
| Reconnecting through another replica converges | `reconnects through a second replica` (timeline compared before/after) | `inspects_memory_and_reconnects` (second window on the alternate endpoint renders an identical timeline) | closed |

All six capabilities are declared in `e2e/coverage.json` with assertion
strings verified to appear in the referenced bodies, and
`scripts/mutate-coverage-gate.sh` confirms the gate still rejects each way a
claim can be false.

## A8.8 — Throughput baseline

| Behavior | Evidence | Verdict |
|---|---|---|
| The documented workload runs on two ultrad and two workers | `TestA88_ThroughputBaseline` asserts the replica and worker counts | closed |
| Machine-readable latency, throughput, retry, and fan-out lag are emitted | the `ultralogical.throughput_baseline.v1` artifact, logged and optionally written to `ULTRA_BASELINE_OUT` | closed |
| Event lag is measured by subscribers that did not start the work | one subscriber per (replica, session) | closed |
| Assertions are invariants plus a generous ceiling | every run completed, no duplicate step index, queue drained, subscribers received events; ceilings are 3 min and 45 s at p99 | closed |
| The artifact names workload, hardware, replicas, and queue config | `agent_docs/throughput_baseline.md` plus the artifact's own `workload` and `hardware` blocks | closed |

The document states plainly that this is a regression baseline and not a
capacity claim, and that comparing artifacts across machines is meaningless.

## A8.9 — Security documentation

`docs/security.md` and `e2e/phase8_security_test.go` were written together, and
each section of the document names the subtests that prove it.

| Claim | Evidence | Verdict |
|---|---|---|
| Grant narrowing is enforced and widening is refused | `narrowing_only_at_start_run`, `grandchild_cannot_escalate` | closed |
| Authority is decided at dispatch, not discovery | `forged` subtests plus `TestA81_EnvironmentGrantEnforcement` | closed |
| Denials are uniform and non-disclosing | `denials_are_uniform_and_opaque` | closed |
| Denials are audited | `denial_emits_an_audit_event` | closed |
| Denials perform no side effect | `denied_side_effect_never_happens` | closed |
| Cross-tenant is indistinguishable from missing | `cross_tenant_reads_are_indistinguishable_from_missing` | closed |
| Credentials never leave the worker, in any encoding | `credentials_never_leave_the_worker` (also asserts the canary has multiple encoded forms, so the sweep is not vacuous) | closed |
| Environment tokens rotate and revoke on restart | `TestA74_EnvDurabilityAndRotation` (Phase 7, referenced not duplicated) | closed |
| Credentials confer no tool, environment, or spawn authority | `narrowed_child_gains_nothing_from_org_credentials` | closed |
| The document claims nothing broader than the tests prove | section 7 enumerates what is *not* claimed: malicious workers, DoS, production key management, and the dev-token authenticator | closed |

## Phase 3 audit rows

Every Phase 3 row in `phases_0_6_audit.md` now names passing evidence and is
marked closed. The Phase 6.7 row that deferred "full races/cohort" to Phase 8
is closed by A8.2 and A8.3.

## Verdict

Phase 8 is closed. A8.1–A8.9 pass with two ultrad and two workers where the
plan specifies; exactly-once wait resolution and at-most-once parent
resumption are enforced by database predicates rather than process state;
every terminal child and parent path routes through the same durable
resolution policy; and every client row is backed by an application-path test
rather than a direct-RPC substitute.

Two privilege-escalation defects were found during the audit and fixed rather
than documented around. Their absence is now asserted, which is the only
version of "fixed" that survives a refactor.
