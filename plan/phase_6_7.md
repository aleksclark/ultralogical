# Phase 6.7 — Security-critical orchestration remediation

**Duration:** bounded remediation · **Depends on:** Phase 6.6

## Goal

Repair the highest-risk correctness and security defects exposed by the Phase 0–6 audit, then hand every remaining audited gap to a completion-scoped successor phase. Phase 6.7 is not the container for the entire historical backlog.

## Scope

**In:**

- Dispatch-time native, environment, and MCP authorization with canonical grants.
- Uniform `PermissionDenied` events for denied in-session tool actions.
- Child-agent creation with parent linkage, narrowed grants, child limits, transactional first-step enqueue, and durable spawn events.
- Durable wait storage and terminal-child fan-in groundwork.
- Queue panic-redelivery conformance.
- Removal of environment-provisioning busy-spin polling.
- Flow invocation provenance groundwork already implemented while auditing.

**Explicitly handed forward:**

- Phase 0–2 completion and evidence integrity: Phase 7.
- Complete cohorts/waits, race matrix, grant acceptance, multi-replica proof, memory/presence/run-tree completion: Phase 8.
- Complete flows: Phase 9.
- Real remote providers: Phase 10.
- Advanced loop, Switchboard, automation, and tracing: Phase 11.
- Production security, billing, operations, retention, queue swap, chaos/load: Phase 12.
- Desktop distribution and release-wide parity: Phase 13.

## Required implementation sequence

1. Define named real-stack tests for each remediation behavior before adding coverage claims.
2. Enforce canonical grants during native/MCP discovery and again during dispatch; emit uniform denial events.
3. Make child creation, spawn event append, and first-step enqueue one transaction with durable uniqueness.
4. Persist waits and ordered members, route every implemented terminal-child completion through one idempotent resolver, and retain unimplemented timeout/cohort races in Phase 8.
5. Add panic-redelivery cases to the shared queue conformance suite and run them unchanged against River and inproc.
6. Replace provisioning busy loops with bounded ticker/deadline behavior and assert cancellation.
7. Update coverage only for bounded behavior that has real Go, web, and GPUI evidence; do not claim successor behavior.
8. Independently audit A6.7.1–A6.7.5 and the successor ownership map before closing the phase.

## Acceptance tests

- **A6.7.1 — Dispatch authorization.** Native and MCP discovery filter denied resources; dispatch rechecks grants; environment termination rejects an ungranted environment; denials emit typed `PermissionDenied` without leaking cross-tenant existence.
- **A6.7.2 — Spawn transaction.** An allowed child is created once with parent ID and subset grants, emits `RunSpawned`, and has its first step transactionally enqueued. Broader grants and child-limit overflow fail without a child or job.
- **A6.7.3 — Wait groundwork.** Wait and ordered membership records are durable; terminal children can resolve an open wait and enqueue parent resumption at most once. The full race/timeout/cohort contract remains Phase 8 until A8.2–A8.3 pass.
- **A6.7.4 — Panic redelivery.** River and inproc redeliver a panicking job according to conformance without losing it.
- **A6.7.5 — Provision polling.** Provisioning waits on a bounded ticker/deadline rather than a CPU busy loop.

## Validation

```sh
buf generate
gofmt -w loop jobqueue testkit/harness
go test ./jobqueue/... -count=1 -timeout 5m
go test ./... -count=1 -timeout 15m
task lint
python3 scripts/verify-coverage.py
git diff --check
```

## Exit criteria

- A6.7.1–A6.7.5 pass in required CI exactly as specified, with named tests and no skipped path.
- `agent_docs/phases_0_6_audit.md` assigns every remaining gap to exactly one successor phase.
- Partial wait/cohort, multi-replica, flow, provider, automation, or UI groundwork is not represented as complete evidence.
- Phase closure does not require successor work, but no successor may remove or weaken an inherited acceptance requirement.
