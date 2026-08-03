# Phase 7 inventory — audit bullet to bounded behavior to named test

Written before production changes, per Phase 7 required sequence step 1. Every
open Phase 0–2 audit bullet is decomposed into bounded observable behaviors, the
production entrypoint that exposes each behavior, and the named test that
asserts it. Behavior with no implementation is marked **unimplemented** so the
phase cannot close by renaming partial work.

Test name legend:

- `go:` Go test in the real-stack functional suite (`e2e/`) or a package suite.
- `web:` Playwright test driving the shipped dark shadcn application.
- `gpui:` Rust test that opens a real GPUI window and inspects rendered state.

## A7.1 — Queue regression and codegen gates (Phase 0)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Job enqueued in an uncommitted tx is never worked; commit makes it visible | `jobqueue.Enqueuer.EnqueueTx` | `go: jobqueue/conformance TransactionalVisibility` |
| Rolled-back enqueue is never delivered, and the backend stores no residue | `jobqueue.Enqueuer.EnqueueTx` | `go: jobqueue/conformance RollbackInvisibility` |
| Failed job attempt counter increments per delivery and stops at MaxAttempts | `jobqueue.Options.MaxAttempts` | `go: jobqueue/conformance RetryAttemptAccounting` |
| Redelivery honors the promised minimum backoff between attempts | backend retry policy | `go: jobqueue/conformance AtLeastOnceRedelivery` |
| Handler panic is redelivered (Phase 6.7 regression, required) | backend worker invoke | `go: jobqueue/conformance PanicRedelivery` |
| A job in flight when the queue stops is redelivered after restart | `jobqueue.Queue.Stop` / `Start` | `go: jobqueue/conformance ShutdownRedelivery` |
| Kinds route only to their registered handler | `jobqueue.Register` | `go: jobqueue/conformance KindRouting` |
| Stale generated Go output fails verification; restored tree passes | `task verify:codegen` | `go: e2e TestA71_CodegenDriftGate` |
| Stale generated TypeScript output fails verification | `task verify:codegen` | `go: e2e TestA71_CodegenDriftGate` |
| Stale generated Rust output fails verification | `task verify:codegen:rust` | `go: e2e TestA71_CodegenDriftGate` |

Both backends run the same suite: `jobqueue/river` and `jobqueue/inproc`.

## A7.2 — Incremental applications (Phase 1)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| One run emits multiple ordered `TextDelta` events before `RunCompleted` | `EventService.Subscribe` | `go: e2e TestA15_TrueStreaming`, `go: e2e TestA72_IncrementalRendering` |
| The browser renders at least two distinct intermediate assistant states | shipped shadcn timeline | `web: renders incremental streamed frames before completion` |
| The GPUI window renders the same progression before completion | GPUI `SessionWindow` | `gpui: renders_incremental_stream_frames` |
| Replay from seq 0 yields the same final timeline in both clients | `EventService.Subscribe(from_seq=0)` | `web: replays identical timeline after reload`, `gpui: replays_identical_timeline` |

## A7.3 — Redaction sweep (Phase 1)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| A planted credential is used successfully by a real run | credential resolution in `loop` | `go: e2e TestA73_RedactionSweep` |
| The canary appears in no RPC response payload | all Connect responses observed in the test | `go: e2e TestA73_RedactionSweep` |
| The canary appears in no event payload or persisted run history | `events`, `agent_runs.history` | `go: e2e TestA73_RedactionSweep` |
| The canary appears in no database diagnostic column | `dev_envs`, `credentials` | `go: e2e TestA73_RedactionSweep` |
| The canary appears in no cored or worker structured log line | process stderr captured by the harness | `go: e2e TestA73_RedactionSweep` |
| The canary appears in no error chain returned to a client | forced failure paths | `go: e2e TestA73_RedactionSweep` |
| Literal and encoded (URL, base64, JSON-escaped) forms are all checked | redactor + assertion helper | `go: e2e TestA73_RedactionSweep`, `go: secrets TestRedactEncodedForms` |
| Application state never holds the canary | web local state, GPUI state | `web: never exposes credential material`, `gpui: never_exposes_credential_material` |

## A7.4 — Environment durability and rotation (Phase 2)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| A provisioned environment keeps its workspace across cored and worker death | `localdocker` named volume | `go: e2e TestA74_EnvDurabilityAndRotation` |
| Reconnecting after restart reads a file written before the restart | `EnvService.ExecPreview` | `go: e2e TestA74_EnvDurabilityAndRotation` |
| `RestartEnv` rotates the environment token and increments epoch | `EnvService.RestartEnv` (**was unimplemented; added in Phase 7**) | `go: e2e TestA74_EnvDurabilityAndRotation` |
| The rotated token authenticates against the environment | `envwork.Service.Restart` | `go: e2e TestA74_EnvDurabilityAndRotation` |
| The pre-rotation token is rejected | Bezalel bearer auth | `go: e2e TestA74_EnvDurabilityAndRotation` |
| A cached MCP client created before rotation can no longer call tools | `mcp.Cache` epoch invalidation (**was unimplemented; added in Phase 7**) | `go: e2e TestA74_EnvDurabilityAndRotation`, `go: mcp TestCacheEpochInvalidation` |
| The restart is visible to shipped clients as an epoch change | env panel | `web: restarts an environment and shows a new epoch`, `gpui: shows_environment_restart_epoch` |

## A7.5 — Failure and reconciliation (Phase 2)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Killing the environment mid-run yields a typed tool failure, not a hang | `loop` MCP tool adapter | `go: e2e TestA75_FailureAndReconciliation` |
| The run reaches the documented terminal outcome after environment loss | step worker outcome classifier | `go: e2e TestA75_FailureAndReconciliation` |
| Terminal event order is `EnvFailed` before the run's terminal event | event log | `go: e2e TestA75_FailureAndReconciliation` |
| Reconciliation does not busy loop (bounded reconcile job count) | `envwork.Service.Reconcile` | `go: e2e TestA75_FailureAndReconciliation` |
| Repeated termination is idempotent and leaves no resource | `EnvService.TerminateEnv` | `go: e2e TestA75_FailureAndReconciliation` |
| Provisioning interrupted by worker death converges without duplicate resources | `envwork.Service.Provision` + reconcile adoption (**was unimplemented; added in Phase 7**) | `go: e2e TestA75_InterruptedProvisioning` |
| A tool call whose environment is gone fails within its deadline | per-call tool timeout | `go: e2e TestA75_FailureAndReconciliation` |

## A7.6 — Metering and tenancy (Phase 2)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Ready-to-terminal usage is bounded by persisted heartbeats | `env_usage.last_metered_at` | `go: e2e TestA76_MeteringAndTenancy` |
| Usage equals lifecycle replay duration within one heartbeat | `UsageStore` + lifecycle events | `go: e2e TestA76_MeteringAndTenancy` |
| Usage survives worker death without unbounded open intervals | reconcile watermark | `go: e2e TestA76_MeteringAndTenancy` |
| An interval closes exactly once even under repeated terminal transitions | `UsageStore.Close` | `go: e2e TestA76_MeteringAndTenancy` |
| Another org gets the same not-found answer for env and usage | `EnvService.GetEnv`, `BillingService.GetUsage` | `go: e2e TestA76_MeteringAndTenancy` |
| Org-scoped usage is visible in both shipped clients | usage view | `web: shows org usage totals`, `gpui: shows_org_usage_totals` |

## A7.7 — Provider conformance (Phase 2)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Provision reaches ready and publishes an endpoint | `EnvProvider.Provision`/`Status`/`Endpoint` | `go: envprovider/conformance Provision` |
| Health check passes on the ready endpoint | `mcp.Healthy` | `go: envprovider/conformance Health` |
| Authenticated MCP discovery lists the expected tools | `mcp.Client.Tools` | `go: envprovider/conformance Discovery` |
| `bash` runs a real command and returns stdout | MCP `bash` | `go: envprovider/conformance Bash` |
| Exact `edit` applies and is observable via `view` | MCP `write`/`edit`/`view` | `go: envprovider/conformance ExactEdit` |
| LSP tool responds with a structured answer (or a typed unavailable result) | MCP `lsp_diagnostics` | `go: envprovider/conformance LSP` |
| A long command becomes a background job whose output is retrievable | MCP `bash(run_in_background)` + `job_output` | `go: envprovider/conformance BackgroundJobAndTimeout` |
| A wrong bearer token is rejected | Bezalel auth | `go: envprovider/conformance TokenRejection` |
| Restart preserves the workspace and rotates the token | `EnvProvider.Restart` | `go: envprovider/conformance RestartRotatesToken` |
| Terminate removes container and volume | `EnvProvider.Terminate` | `go: envprovider/conformance Terminate` |
| Repeated terminate is idempotent | `EnvProvider.Terminate` | `go: envprovider/conformance Terminate` |
| No labeled container or volume leaks after the suite | Docker label `ultralogical.env_id` | `go: envprovider/conformance LeakCheck` |

## A7.8 — Shipped surfaces and dev stack (Phase 1)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| The web app is built from reusable shadcn components in a dark theme | `ui/web/src/components/ui/*` | `web: renders shadcn primitives in dark mode` |
| Session, run, environment, and usage behavior is driven through shipped web controls | shadcn views | `web` suite (session, environment, usage specs) |
| A real GPUI window renders session list, timeline, connection state | GPUI `App` entrypoint (**was unimplemented; added in Phase 7**) | `gpui: renders_session_list_and_timeline` |
| GPUI tests invoke the same actions as the native entrypoint | `DesktopWindow::start_up`, called by `main.rs` | `gpui: drives_same_actions_as_entrypoint` |
| `task dev` starts the documented usable stack in one command | `scripts/dev-stack.sh` | `go: e2e TestA78_DevStackSmoke` |
| The stack smoke creates a session, streams a run, provisions an env, shuts down clean | `scripts/dev-stack.sh smoke` | `go: e2e TestA78_DevStackSmoke` |
| No owned process or container survives the smoke | smoke teardown | `go: e2e TestA78_DevStackSmoke` |

## A7.9 — Evidence integrity (cross-phase)

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Coverage verification rejects a reference to a nonexistent test file | `scripts/verify-coverage.py` | `go: e2e TestA79_EvidenceIntegrity` |
| Coverage verification rejects a test not executed by required CI | `scripts/verify-coverage.py` CI-execution check | `go: e2e TestA79_EvidenceIntegrity` |
| Coverage verification rejects omnibus references without capability assertions | assertion-tag requirement | `go: e2e TestA79_EvidenceIntegrity` |
| Coverage verification rejects desktop evidence that bypasses GPUI | GPUI-path requirement | `go: e2e TestA79_EvidenceIntegrity` |
| The unmutated repository passes verification | `python3 scripts/verify-coverage.py` | `go: e2e TestA79_EvidenceIntegrity` |

## Explicitly unimplemented before Phase 7

These had no production behavior at the start of the phase and are built here,
not renamed:

1. `EnvService.RestartEnv` and env token rotation with epoch bump.
2. MCP client caching keyed by environment epoch, with invalidation.
3. Reconciler adoption of interrupted provisioning (no duplicate resources).
4. Usage replay verification from lifecycle events.
5. A rendered GPUI application window and its application-path test harness.
6. One-command dev stack with a noninteractive smoke and leak checks.
7. CI-execution and GPUI-path checks inside coverage verification.
8. Codegen drift mutation gate covering Go, TypeScript, and Rust output.

## Still owned by later phases

Child cohorts and multi-replica orchestration (Phase 8), flows (Phase 9), real
remote providers (Phase 10), advanced loop completion (Phase 11), production
auth/billing (Phase 12), and desktop distribution (Phase 13). Nothing in this
inventory claims those rows.
