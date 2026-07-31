# Phase 8 — Durable orchestration and distributed sessions

**Duration:** 2–3 weeks · **Depends on:** Phase 7

## Goal

Close every audited Phase 3 gap. Agent trees, cohorts, waits, grants, memory, presence, and subscriptions must remain correct across races, retries, process death, and multiple ultrad and worker replicas.

## Scope

**In:**

- `spawn_agent`, `wait_for_agents`, and `run_agent_cohort` as complete durable orchestration tools.
- Monotonically decreasing grants enforced during discovery and dispatch, including environment and MCP access.
- Durable wait timeouts, cancellation/failure resolution, pre-commit child-completion races, exact tool-call result correlation, and child results.
- A real multi-replica harness with round-robin ingress, direct replica clients, indexed process control, health checks, and complete cleanup.
- Presence, replay, memory caps/concurrency, run trees, lanes, and throughput proof.
- Dark shadcn and rendered GPUI application surfaces for run trees, waits, grants, memory, and replica reconnect behavior.
- Security and orchestration documentation verified against executable scenarios.

**Out:** flows (Phase 9), remote providers (Phase 10), advanced loop features (Phase 11), and production-scale chaos/load (Phase 12).

## Required implementation sequence

1. Inventory every Phase 3 acceptance bullet and state transition. Name bounded Go, Playwright, and GPUI tests before changing coverage declarations.
2. Finish durable child creation with parent linkage, invocation provenance where present, transactional first-job enqueue, max-child enforcement, idempotent retry, and persisted final result.
3. Finish waits as a transactional state machine. Persist cohort membership and the originating Fantasy tool-call ID; inject exactly one matching tool-result message when all members terminate or timeout.
4. Resolve all races: a child terminal before wait commit, duplicate child terminal notifications, timeout racing the last child, parent cancellation, child failure, parent process death, and queue redelivery.
5. Implement `run_agent_cohort` as server-side fan-out/fan-in using the same child and wait primitives, not prompt conventions or polling in the model.
6. Exercise grant narrowing through real tools. Discovery must hide denied tools; forged calls and stale discovered calls must fail at dispatch and emit `PermissionDenied` without leaking resource existence.
7. Complete the multi-replica harness. `WithReplicas(2,2)` must start exactly two healthy ultrad and two workers, expose load-balanced and direct URLs, permit indexed kill/restart, and clean every process once.
8. Prove cross-replica event delivery and worker takeover with concurrent runs. Queue-depth helpers must count only the runnable job kinds named by a test.
9. Prove memory key/value limits, atomic concurrent writes, namespace rules, replay, and cross-tenant isolation through the real stack.
10. Build run-tree, cohort/wait, memory, presence, and reconnect views/actions in the dark shadcn and GPUI applications.
11. Record a reproducible throughput baseline with workload, hardware, replica count, queue configuration, and event-lag distribution. It is a regression baseline, not an unsupported capacity claim.
12. Independently audit Phase 3 APIs, events, database invariants, clients, and failure paths. Any unproven race keeps the phase open.

## Acceptance tests

- **A8.1 — Spawn durability and grants.** A parent spawns an allowed child and a narrower grandchild. Persisted grants are strict subsets, denied tools/envs are absent from discovery, forged denied native and MCP dispatches return uniform errors and emit `PermissionDenied`, and allowed operations succeed. Retry produces one child and one first step.
- **A8.2 — Wait race matrix.** Cover child completion before and after parent wait commit, duplicate completion delivery, mixed completed/failed/cancelled children, parent cancellation, timeout before/after the last child, and worker death. Each case resolves one wait, injects one result matching the original tool-call ID, and resumes the parent at most once.
- **A8.3 — Cohort fan-out/fan-in.** A cohort launches multiple children concurrently, preserves declaration order in its aggregate result, exposes per-child terminal state/result, handles one failed member according to the documented policy, and resumes without model-side polling.
- **A8.4 — Cross-replica subscription.** Subscribe through replica A, append and start through replica B, then restart either replica. Direct and load-balanced clients converge on an identical gap-free, duplicate-free event sequence via resume-by-seq.
- **A8.5 — Worker takeover.** Four concurrent multi-step runs execute on two workers. Kill worker 0 mid-step; worker 1 completes the workload with unique `(run_id, step_index)`, correct terminal states, bounded redelivery, and no event gaps.
- **A8.6 — Memory and presence.** Concurrent writes obey caps and atomicity, reconnect replay reconstructs identical memory/presence state, stale presence expires as documented, and cross-tenant access is indistinguishable from missing.
- **A8.7 — Application run trees.** Web and GPUI launch or observe a parent/cohort, render parent/child linkage and interleaved lanes, show awaiting/timeout/failure transitions, inspect memory, reconnect through another replica, and converge on the API event log.
- **A8.8 — Throughput baseline.** The documented workload runs on two ultrad/two workers and emits machine-readable latency, throughput, retry, and fan-out-lag measurements. Tests assert invariants and a generous regression ceiling rather than environment-dependent marketing numbers.
- **A8.9 — Security documentation.** Executable examples prove grant narrowing, token scope, denial visibility, and noninheritance of integration credentials; docs contain no broader claim than the tests.

## Validation commands

```sh
task generate
task verify:codegen
task lint
go test ./jobqueue/... -count=1 -timeout 5m
go test ./e2e -run 'TestA3|TestA8' -count=1 -timeout 15m
task test:functional
task web:test
cargo test --manifest-path ui/desktop/Cargo.toml
python3 scripts/verify-coverage.py
git diff --check
```

Run the distributed suite repeatedly with process-kill timing jitter. A race test that only passes with sleeps or fixed ordering is not acceptance evidence.

## Exit criteria

- A8.1–A8.9 pass with two ultrad and two workers where specified.
- Database uniqueness and locking, not in-memory flags, enforce exactly-once wait resolution and at-most-once parent resumption.
- Every terminal child and parent path invokes the same durable resolution policy.
- Every Phase 3 audit bullet is closed with bounded real-stack and application-path evidence; no direct-RPC desktop test is labeled GPUI evidence.
