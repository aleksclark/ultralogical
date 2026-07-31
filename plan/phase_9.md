# Phase 9 — Complete versioned flows

**Duration:** 2 weeks · **Depends on:** Phase 8

## Goal

Close every audited Phase 4 gap. A flow must be a validated, versioned, reproducible declaration that provisions its environments, waits for readiness, starts the declared agent topology with exact grants and provenance, and is fully operable from API, CLI, web, and GPUI.

## Scope

**In:**

- Structured flow validation with stable field paths and typed errors.
- Immutable versions, deterministic parameter rendering, and invocation provenance on runs, environments, and events.
- Environment declarations, agent-to-environment wiring, readiness gating, spawn topology, and rollback/cleanup on partial failure.
- A real `ultra` CLI for flow put/get/list/validate/invoke/status commands.
- Dark shadcn and GPUI flow catalog, editor/validation, invocation, progress, topology, and provenance surfaces.
- Examples and documentation executed against the real stack.

**Out:** remote provider implementations (Phase 10), advanced automation (Phase 11), and production auth/billing (Phase 12).

## Required implementation sequence

1. Reconcile the Phase 4 plan, current flow schema, handlers, persistence, events, and all first-party clients. Define one acceptance row per observable behavior before coverage changes.
2. Define additive proto/schema changes for environment declarations, topology, grants, readiness policy, and provenance. Reject unknown references, cycles where forbidden, duplicate names, invalid templates, invalid grants, and unsupported provider capabilities before persistence or invocation.
3. Preserve immutable versions. Concurrent writes to the same `(org, name, version)` must converge on one documented result without overwriting prior definitions.
4. Render parameters deterministically and persist the rendered prompt/config used by each run and environment so later flow edits cannot alter replay.
5. Create invocation, environment, run, provenance events, and first jobs transactionally where possible. Persist explicit invocation progress and terminal reason.
6. Provision declared environments, wait for all required readiness checks, then launch the declared roots and child topology. No agent may start early or receive an undeclared environment.
7. On provisioning, validation-after-load, cancellation, or launch failure, converge invocation state and clean only resources owned by that invocation. Retry must not duplicate environments or runs.
8. Implement `cmd/ultra` with generated clients and machine-readable output. It must not bypass public APIs or read Postgres directly.
9. Implement web and GPUI catalog, version selection, structured validation display, parameter form, invocation progress/topology, links to runs/environments, cancellation, and provenance.
10. Add executable examples for single-agent, environment-backed, and multi-agent flows. Documentation commands run in CI against the harness.
11. Independently compare the complete flow definition language and lifecycle against implementation and evidence. Partial support may not be called a completed flow feature.

## Acceptance tests

- **A9.1 — Versioning and validation.** Put valid versions, reject overwrite, and reject every invalid reference/template/topology/grant with stable typed field paths. Cross-org flow IDs are indistinguishable from missing. CLI, web, and GPUI render the same validation errors.
- **A9.2 — Deterministic provenance.** Invoke a parameterized flow, then modify the source by creating a later version. The invocation, runs, environments, rendered prompts, grants, and events retain the exact original flow/version/invocation IDs and rendered values on replay.
- **A9.3 — Readiness gate.** A flow declares two environments and multiple agents. No run starts until all required environments pass health; each run receives only declared environments. Delayed readiness is visible in API, web, and GPUI.
- **A9.4 — Topology.** Root and child agents start in the declared topology, preserve parent and invocation links, use durable cohort/wait semantics where declared, and produce a reproducible terminal invocation result.
- **A9.5 — Failure convergence.** One environment fails provisioning and another succeeds. The invocation reaches the documented terminal state, agents do not start, owned resources are cleaned exactly once, retries create no duplicates, and unrelated session resources remain untouched.
- **A9.6 — Cancellation and replay.** Cancel during provisioning and during execution. Replay reconstructs ordered progress, cleanup, run/environment terminals, and final invocation state without gaps.
- **A9.7 — CLI parity.** CLI validate/put/list/get/invoke/status/cancel operate solely through public APIs, support JSON output, return nonzero on typed failures, and match API state.
- **A9.8 — Application parity.** Dark shadcn and GPUI users create or select a version, correct structured validation errors, invoke with parameters, observe readiness/topology/provenance, open linked resources, cancel, reconnect, and recover the same state.
- **A9.9 — Executable documentation.** Every shipped flow example validates and completes against the harness; copied identifiers and expected event sequences are checked rather than merely compiled.

## Validation commands

```sh
task generate
task verify:codegen
task lint
go test ./e2e -run 'TestA4|TestA9' -count=1 -timeout 15m
task test:functional
go test ./cmd/ultra/... -count=1
task web:test
cargo test --manifest-path ui/desktop/Cargo.toml
python3 scripts/verify-coverage.py
git diff --check
```

## Exit criteria

- A9.1–A9.9 pass in required CI.
- Every persisted run and environment created by a flow carries immutable invocation provenance.
- Readiness, topology, retries, cancellation, and partial failure are durable state machines, not handler-local orchestration.
- Every Phase 4 audit bullet and documented example is closed by bounded API, CLI, web, and rendered GPUI evidence.
