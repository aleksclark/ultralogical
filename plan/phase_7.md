# Phase 7 — Foundations, evidence, and dev-environment completion

**Duration:** 2–3 weeks · **Depends on:** Phase 6.7

## Goal

Close every audited Phase 0–2 gap and establish application-path evidence that later phases can extend. This phase is complete only when the original contracts work through the real distributed stack, the dark shadcn web application, and an actual dark GPUI window.

## Scope

**In:**

- Phase 0 deliberate codegen-drift gate mutation tests and regression execution of the queue conformance contract established in Phase 6.7.
- Phase 1 browser incremental-rendering proof, complete secret redaction sweep, complete shadcn conversion, and one-command full development stack.
- A real GPUI application shell with a window, session list/timeline, connection state, and test-driving affordances. Headless tonic or reducer tests remain useful but are not UI evidence.
- Phase 2 Docker durability, token rotation and cache invalidation, continued-run failure behavior, reconciliation, metering, tenancy, usage surfaces, and complete local-provider conformance.
- Evidence-gate changes needed to reject missing files, nonexecuted tests, unbounded omnibus evidence, and direct-RPC claims presented as UI evidence.

**Out:** child cohorts and multi-replica orchestration (Phase 8), flows (Phase 9), remote providers (Phase 10), advanced loop features (Phase 11), production auth/billing (Phase 12), and desktop distribution (Phase 13).

## Required implementation sequence

1. Write an inventory mapping every open Phase 0–2 audit bullet to bounded observable behaviors, production entrypoints, and named Go/web/GPUI tests. Mark missing behavior as unimplemented.
2. Make generated-code verification fail under a temporary checked-in-proto/generated-output mismatch, then restore the tree and prove the normal gate passes.
3. Extend queue conformance for retry attempt accounting, backoff, transactional visibility, rollback invisibility, and shutdown/redelivery on both River and inproc; retain Phase 6.7 panic redelivery as a required regression.
4. Convert the shipped web surface to reusable shadcn components and assert intermediate streamed frames rather than only final text.
5. Create and render the GPUI window. UI tests must invoke the same actions and inspect the same rendered/reduced state as the native entrypoint.
6. Run a canary-secret sweep across RPC responses, events, persisted histories, structured logs, error chains, and traces available in this phase.
7. Prove local Docker environment durability independently of ultrad and worker process lifetime. Restart the worker, rotate the environment token, reject the old token, and ensure cached MCP clients cannot keep using it.
8. Complete reconciler and failure semantics: dead environment detection, repeated termination idempotency, interrupted provisioning recovery, tool timeout, and a run that continues or fails with the documented typed outcome after environment loss.
9. Make the usage ledger crash-bounded and replayable from lifecycle events. Add org-scoped usage API, web view, and GPUI view with cross-tenant indistinguishability.
10. Make `task dev` start the documented usable stack and add a noninteractive smoke that creates a session, streams a run, provisions an environment, and shuts down without leaked processes or containers.
11. Update `e2e/coverage.json` only after each referenced test exists, runs in required CI, and asserts its bounded behavior.
12. Perform an independent phase audit from proto descriptors, events, lifecycle states, UI actions, and the Phase 0–2 plans. Unmet rows keep the phase open.

## Acceptance tests

- **A7.1 — Queue regression and codegen gates.** River and inproc pass the same queue conformance suite, including the Phase 6.7 panic-redelivery regression and Phase 7 rollback/retry/shutdown cases. A scripted mutation proves stale generated Go, TypeScript, or Rust output fails verification; the restored tree passes.
- **A7.2 — Incremental applications.** One scripted run emits multiple text deltas. Go observes ordered events, Playwright observes at least two distinct intermediate rendered states, and GPUI application-path evidence observes the same progression before completion. Replay produces the same final timeline.
- **A7.3 — Redaction.** A planted credential is used successfully but cannot be found in API payloads, event payloads, run history, database diagnostic fields, logs, errors, or application state. The test checks the literal and encoded forms.
- **A7.4 — Environment durability and rotation.** Provision and edit a file, kill and restart ultrad and worker, reconnect to the same environment, and read the file. Restart the environment, verify a new token works, the prior token fails, and a pre-rotation cached client also fails.
- **A7.5 — Failure and reconciliation.** Kill the environment during a multi-step run and assert the documented typed tool/run outcome, terminal event order, no busy loop, and successful idempotent cleanup. Interrupt provisioning and prove reconcile converges without duplicate resources.
- **A7.6 — Metering and tenancy.** Ready-to-terminal usage is bounded by persisted heartbeats, equals replayed lifecycle duration within one heartbeat, survives process death, and closes once. Another org receives the same not-found response as a missing environment or usage record.
- **A7.7 — Provider conformance.** Local Docker passes provision, health, authenticated MCP discovery, bash, exact edit, LSP, timeout/background job, token rejection, restart, terminate, repeated terminate, and leak checks against a real Bezalel container.
- **A7.8 — Shipped surfaces and dev stack.** The dark shadcn web app and dark GPUI window expose session/run/environment/usage behavior through shipped clients. The one-command stack smoke completes and leaves no owned process or container.
- **A7.9 — Evidence integrity.** Mutation tests prove coverage verification rejects nonexistent tests, tests absent from CI, duplicate omnibus references without capability assertions, and desktop evidence that bypasses GPUI.

## Validation commands

```sh
task generate
task verify:codegen
task lint
task test
task test:functional
task web:build
task web:test
cargo test --manifest-path ui/desktop/Cargo.toml
python3 scripts/verify-coverage.py
git diff --check
```

Run the codegen and evidence mutation suites separately so the test proves each gate fails for the intended reason before restoring the working tree.

## Exit criteria

- A7.1–A7.9 pass in required CI with no skipped capability path.
- Every Phase 0–2 audit bullet is either closed by named evidence or still visibly open; the phase cannot close with an open bullet.
- Web evidence drives rendered shadcn controls and GPUI evidence drives a rendered GPUI application. Direct HTTP, tonic, reducer-only, compilation, and control-presence tests cannot substitute.
- The independent audit finds no public Phase 0–2 behavior missing success, material failure, replay/durability, tenancy, and supported-client evidence.
