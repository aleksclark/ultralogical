# Phase 7 independent audit

Performed after implementation, from proto descriptors, event variants,
lifecycle states, client actions, CI configuration, and the Phase 0–2 plans —
not from the implementation narrative. Each row states the bounded behavior, the
evidence that proves it, and its verdict.

Evidence must be a named test that runs in required CI and asserts the behavior.
"Closed" means every bullet's success, material failure, replay/durability,
tenancy, and supported-client paths are asserted where the capability has them.

## A7.1 — Queue regression and codegen gates

| Behavior | Evidence | Verdict |
|---|---|---|
| Transactional visibility on both backends | `jobqueue/conformance TransactionalVisibility` via river + inproc suites | closed |
| Rollback leaves no trace, proven against a live queue | `RollbackInvisibility` (uses a committed control job) | closed |
| Retry attempts bounded by MaxAttempts, with backoff between each | `RetryAttemptAccounting` | closed |
| Redelivery honors the promised minimum backoff | `AtLeastOnceRedelivery` | closed |
| Panic redelivery (Phase 6.7 regression) | `PanicRedelivery` | closed |
| A job in flight survives queue shutdown and is redelivered | `ShutdownRedelivery` | closed |
| Kind routing | `KindRouting` | closed |
| Stale Go/TS output fails verification; restored tree passes | `e2e TestA71_CodegenDriftGate` → `scripts/mutate-codegen-gate.sh` mutations 1–2 | closed |
| Rust generated surface cannot silently drop a proto | mutation 3 + `scripts/verify-codegen-rust.py` in CI | closed |

Note: `verify-codegen.sh` distinguishes "drifted" (exit 1) from "generator
unavailable" (exit 2) so remote-plugin rate limiting can never be reported as a
passing or failing drift verdict.

## A7.2 — Incremental applications

| Behavior | Evidence | Verdict |
|---|---|---|
| Multiple ordered deltas strictly before the terminal event | `e2e TestA72_IncrementalRendering` (asserts strictly increasing delta indexes and no delta after terminal) | closed |
| Browser renders at least two distinct intermediate states | `web: renders incremental streamed frames before completion` (distinct painted texts + folded frame counter) | closed |
| GPUI renders the same progression before completion | `gpui: renders_incremental_stream_frames` (counts only painted assistant rows) | closed |
| Replay yields the same final timeline in Go, web, and GPUI | `TestA72_IncrementalRendering`, `web: replays identical timeline after reload`, `gpui: replays_identical_timeline` | closed |

## A7.3 — Redaction

| Behavior | Evidence | Verdict |
|---|---|---|
| The canary is genuinely used (sweep is not vacuous) | `TestA73_RedactionSweep` asserts a vendor call carried it | closed |
| Absent from event payloads, run history, RPC responses, credential ciphertext, environment diagnostics, process logs, and error chains | `TestA73_RedactionSweep` | closed |
| Literal and encoded forms are all checked | `secrets.Encodings` shared by production redaction and the sweep; `secrets TestRedactEncodedForms` | closed |
| Absent from web and GPUI application state | `web: never exposes credential material`, `gpui: never_exposes_credential_material` | closed |

## A7.4 — Environment durability and rotation

| Behavior | Evidence | Verdict |
|---|---|---|
| Workspace survives control-plane death, and the file is readable after restart | `e2e TestA74_EnvDurabilityAndRotation` | closed |
| `RestartEnv` rotates the token and increments epoch | same test (asserts a different decrypted token and a higher epoch) | closed |
| Rotated token works; prior token is rejected | same test | closed |
| A client cached before rotation cannot keep working | same test (Initialize and Call both fail) plus `mcp TestCacheEpochInvalidation` | closed |
| Restart is visible in both clients as a new epoch | `web: restarts an environment and shows a new epoch`, `gpui: shows_environment_restart_epoch` | closed |

## A7.5 — Failure and reconciliation

| Behavior | Evidence | Verdict |
|---|---|---|
| Environment loss mid-run yields a documented terminal outcome, not a hang | `e2e TestA75_FailureAndReconciliation` | closed |
| `EnvFailed` carries a structured reason and precedes the run's failure | same test | closed |
| Reconciliation does not busy loop on a failed environment | same test (queue drains to zero) | closed |
| Repeated termination is idempotent and leaks no container or volume | same test via `ultra.EnvResourceLister` | closed |
| Tool calls are bounded by a deadline | `loop` per-call timeout; `envprovider/conformance PerCallDeadline` | closed |
| Interrupted provisioning converges without duplicate resources | `e2e TestA75_InterruptedProvisioning` (asserts exactly one container) | closed |

## A7.6 — Metering and tenancy

| Behavior | Evidence | Verdict |
|---|---|---|
| Exactly one interval per environment life, closed once | `e2e TestA76_MeteringAndTenancy` (repeated close does not move it) | closed |
| Bounded by persisted heartbeats and by replayed lifecycle duration | same test (compares against `ready_at`/`terminated_at`) | closed |
| Survives worker death without an unbounded open interval | same test (kills the worker mid-life, closes at watermark) | closed |
| Correct org, instance, and rate class | same test | closed |
| Cross-tenant and missing-record denials are indistinguishable | same test (`assertSameDenial` compares code and message) | closed |
| Org usage visible in both clients | `web: shows org usage totals`, `gpui: shows_org_usage_totals` | closed |

## A7.7 — Provider conformance

Provision, health, authenticated discovery of every required tool, `bash`,
exact `edit` including a failing non-match, LSP, background job with retrievable
output, caller deadline, wrong- and missing-token rejection, restart with
workspace persistence and rotation, terminate, idempotent repeat terminate, leak
check, and concurrent provisioning with distinct endpoints — all in
`envprovider/conformance`, run against a real Bezalel container by
`envprovider/localdocker`. **Closed.**

## A7.8 — Shipped surfaces and dev stack

| Behavior | Evidence | Verdict |
|---|---|---|
| Web app built from reusable shadcn primitives in dark mode | `ui/web/src/components/ui/*`; `web: renders shadcn primitives in dark mode` | closed |
| Session, run, environment, and usage behavior driven through shipped web controls | web session/environment specs | closed |
| A real GPUI window renders session list, timeline, connection state, environments, and usage | `gpui: renders_dark_application_shell`, `renders_session_list_and_timeline` | closed |
| GPUI tests invoke the same actions as the native entrypoint | `build_window` + `DesktopClient` shared by `main.rs` and `tests/support`; `gpui: accepts_prompt_keystrokes`, `shows_environment_restart_epoch` | closed |
| `task dev` starts the documented usable stack in one command | `scripts/dev-stack.sh` (Postgres, local model, seeded org/user/provider/credential, ultrad, worker, web) | closed |
| Noninteractive smoke creates a session, streams a run, provisions an environment, and shuts down clean | `e2e TestA78_DevStackSmoke` + the `dev-stack` CI job | closed |
| No owned process or container survives the smoke | same test (container and process leak checks) plus the CI leak assertion | closed |

## A7.9 — Evidence integrity

| Behavior | Evidence | Verdict |
|---|---|---|
| Rejects a nonexistent test file | `e2e TestA79_EvidenceIntegrity` → mutation 1 | closed |
| Rejects a test name the file does not declare | mutation 2 | closed |
| Rejects a reference whose test does not assert the capability | mutation 3 | closed |
| Rejects evidence required CI never executes | mutation 4 | closed |
| Rejects desktop evidence that bypasses rendered GPUI | mutation 5 | closed |
| The unmutated matrix passes | same test, final step | closed |

## Behavior built in this phase that did not exist before

1. `EnvService.RestartEnv`, token rotation, and epoch propagation into events.
2. `mcp.Cache` with epoch keying and local revocation (`mcp.ErrRevoked`).
3. Provisioning handle persistence, `ultra.EnvAdopter`, and reconciler recovery
   of interrupted provisioning.
4. `UsageStore.CloseAtWatermark`/`ListOpen` and watermark-bounded recovery.
5. `ultra.EnvResourceLister` and provider leak checks.
6. A rendered GPUI application (`window.rs`, `state.rs`, `client.rs`,
   `runtime.rs`) with a GPUI-path test harness.
7. Reusable shadcn primitives and the environment/usage/connection surfaces.
8. One-command dev stack, local model endpoint, seeding, and smoke.
9. Coverage verification of assertion semantics, CI execution, and GPUI paths,
   plus both gate mutation suites.
10. Encoded-form redaction and harness log capture.

## Remaining open work (owned by later phases, not by Phase 7)

Child cohorts and multi-replica orchestration (Phase 8), flows (Phase 9), real
remote providers (Phase 10), advanced loop completion (Phase 11), production
auth/billing/retention (Phase 12), and desktop packaging and release-wide proof
(Phase 13). Nothing above claims those rows.

## Verdict

No Phase 0–2 audit bullet owned by Phase 7 remains open. Every closed row names
a test that runs in required CI and asserts the bounded behavior, including
material failure, replay or durability, tenancy, and both supported clients
where the capability is user-visible.
