# Phases 0–6 implementation audit

The independent audit found material planned work still incomplete after Phase 6.6. Phase 6.7 owned the security-critical remediation; completion-scoped Phases 7–11 own the remaining historical backlog.

**Phases 7 and 8 are closed.** Every Phase 0–3 row below now names passing evidence; see [phase_7_audit.md](phase_7_audit.md) and [phase_8_audit.md](phase_8_audit.md) for the independent verifications. Phase 4–6 rows remain open and are owned by Phases 9–11.

## Phase 6.7 remediation status

Implementation is present or substantially present, but acceptance remains pending until each owning test passes. Phase 7 did not adopt these rows:

| Remediation | Status | Owning acceptance |
|---|---|---|
| Dispatch-time environment/tool filtering, native grants, termination authorization, and `PermissionDenied` events | implementation present, acceptance pending | A6.7.1 |
| Child creation with parent linkage, transactional enqueue, narrowed grants, child limits, and `RunSpawned` | implementation present, acceptance pending | A6.7.2 |
| Durable wait records and terminal-child resumption groundwork | completed in Phase 8: full race matrix and cohort fan-in | A6.7.3, then A8.2–A8.3 — **closed** |
| Queue panic redelivery for River and inproc | implementation present, acceptance pending | A6.7.4 |
| Non-busy provisioning polling | implementation present, acceptance pending | A6.7.5 |
| Flow invocation provenance groundwork | partial implementation; complete flow provenance remains Phase 9 | A9.2 |

No row is closed merely because code exists. Phase 6.7 closes only under its plan exit criteria.

## Authoritative ownership

| Original phase | Remaining audited work | Owning acceptance IDs |
|---|---|---|
| Phase 0 | Deliberate generated-code drift mutation gate. Queue retry/rollback/shutdown regression semantics beyond A6.7.4. | A7.1 — **closed** |
| Phase 1 | Browser and GPUI incremental rendering. | A7.2 — **closed** |
| Phase 1 | Complete secret redaction sweep. | A7.3 — **closed** |
| Phase 1 | Complete shadcn conversion, real GPUI foundation, and one-command full stack. | A7.8 — **closed** |
| Phase 2 | Docker durability and restart token/cache invalidation. | A7.4 — **closed** |
| Phase 2 | Continued-run environment failure and reconciliation. | A7.5 — **closed** |
| Phase 2 | Metering, usage, and tenancy evidence. | A7.6 — **closed** |
| Phase 2 | Complete local-provider conformance. | A7.7 — **closed** |
| Cross-phase evidence | Reject false coverage claims and direct-RPC UI substitutes. | A7.9 — **closed** |
| Phase 3 | Spawn durability and real grant-enforcement acceptance. | A8.1 — **closed** |
| Phase 3 | Wait timeout, exact result correlation, cancellation/failure, race, and duplicate-delivery matrix. | A8.2 — **closed** |
| Phase 3 | Complete `run_agent_cohort` fan-out/fan-in. | A8.3 — **closed** |
| Phase 3 | Multi-replica subscriptions and worker takeover. | A8.4–A8.5 — **closed** |
| Phase 3 | Memory, concurrency, presence, replay, and tenancy. | A8.6 — **closed** |
| Phase 3 | Run-tree/lanes/memory application surfaces. | A8.7 — **closed** |
| Phase 3 | Reproducible throughput baseline and security documentation. | A8.8–A8.9 — **closed** |
| Phase 4 | Structured validation and immutable versions. | A9.1 |
| Phase 4 | Run/environment/invocation provenance and deterministic rendering. | A9.2 |
| Phase 4 | Environment wiring, readiness gating, and topology. | A9.3–A9.4 |
| Phase 4 | Partial failure, cleanup, idempotent retry, cancellation, and replay. | A9.5–A9.6 |
| Phase 4 | CLI, complete shadcn/GPUI clients, examples, and executable docs. | A9.7–A9.9 |
| Phase 5 | Shared provider conformance without loopback aliases. | A10.1 |
| Phase 5 | Real Kubernetes BYO and hosted-EKS isolation/quotas. | A10.2–A10.3 |
| Phase 5 | Real Nomad and tunnel-local adapters. | A10.4–A10.5 |
| Phase 5 | Credential dry runs, rotation, redaction, and tenancy. | A10.6 |
| Phase 5 | Provider applications, required CI topology, onboarding, and static wiring. | A10.7–A10.9 |
| Phase 6 | Real Switchboard semantics. | A11.1 |
| Phase 6 | True summarization compaction and sticky fallback with attempt audit. | A11.2–A11.3 |
| Phase 6 | Durable cursor hooks and auto-title. | A11.4 |
| Phase 6 | Actual periodic firing scheduler and native management tool. | A11.5 |
| Phase 6 | OTLP correlation and redaction. | A11.6 |
| Phase 6 | Advanced shadcn/GPUI control surfaces and live-LLM contract. | A11.7–A11.8 |
| Phase 6 | Restart/duplicate-delivery proof across all advanced behaviors. | A11.9 |

Production authentication, billing, retention, queue-swap proof, graceful operations, and chaos/load remain product work rather than historical audit gaps; they are now [Phase 12](../plan/phase_12.md). Desktop packaging and release-wide cross-application proof are [Phase 13](../plan/phase_13.md).

## Evidence integrity requirements

Rust evidence once drove tonic or a headless state core rather than a rendered GPUI application, several web tests asserted controls or final states without the claimed lifecycle behavior, and coverage validation checked declarations more strongly than assertion semantics. Phase 7 closed all three: `scripts/verify-coverage.py` now requires declared assertion strings to appear in the referenced test, requires required CI to execute it, and rejects Rust rows that never inspect a rendered GPUI frame. `scripts/mutate-coverage-gate.sh` proves each rejection in CI.

For every completion phase:

1. Define bounded observable behaviors and named tests before production changes.
2. Prove backend durability and event behavior through the generated Go API against the real stack.
3. Prove visible behavior through the shipped dark shadcn application and an actual dark GPUI application path.
4. Include material failure, replay/restart, race, and tenancy paths.
5. Add `e2e/coverage.json` entries only after referenced tests exist and run in required CI.
6. Perform an independent plan-versus-implementation audit. An open bullet keeps its owning phase open.

Compilation, direct RPC desktop tests, reducer-only tests, rendered control presence, final-state-only smoke tests, aliases standing in for adapters, CRUD standing in for execution, and broad tests that do not assert the named behavior are not completion evidence.
