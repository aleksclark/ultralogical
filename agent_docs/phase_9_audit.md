# Phase 9 independent audit

Performed after implementation, from the migration files, proto descriptors,
invocation state transitions, client test bodies, CI configuration, and the
Phase 4 plan bullets — not from the implementation narrative. Each row states
the bounded behavior, the evidence that proves it, and its verdict.

Evidence must be a named test that runs in required CI and asserts the
behavior. "Closed" means the success path, the material failure path, the
durability/replay path, and the tenancy path are all asserted where the
capability has them. A row that only compiles, only exists, or is only
plausible is open.

## A9.1 — Versioning and validation

| Behavior | Evidence | Verdict |
|---|---|---|
| Auto-assignment produces ascending versions | `TestA91_VersioningAndValidation` | closed |
| An explicit rewrite of an existing version is refused with `already_exists` | same test (also re-reads version 1 to prove it did not change) | closed |
| A stored version reads back byte-for-byte | same test; `flows.definition` is `text`, not `jsonb`, so normalization cannot silently rewrite it | closed |
| Concurrent unversioned writes converge on distinct ascending versions | `TestA91_ConcurrentVersionConvergence` (8 writers; asserts every version 1..8 exists, each with a distinct definition) | closed |
| Every invalid class is rejected with stable typed field paths | `TestA91_ValidationWall` (11 classes, each asserted through both `PutFlow` and `ValidateFlow`) and `TestValidateFlowDefinitionRejectsEveryFailureClass` (28 classes, unit) | closed |
| Nothing is persisted by a rejection | `TestA91_ValidationWall` (lists the catalog after every rejection) | closed |
| Error ordering is stable | `TestFlowValidationErrorsAreOrderedStably` | closed |
| A cross-org flow is indistinguishable from a missing one | `TestA91_CrossOrgFlowIsNotFound` (compares codes *and* messages across three probes) | closed |
| The CLI renders the same field paths | `TestFlowValidateReportsTypedErrors` | closed |
| The web application renders the same field paths | `flows.spec.ts "rejects an invalid flow with typed field paths"` | closed |
| The GPUI window renders the same field paths | `flow_e2e.rs renders_flow_validation_errors` | closed |

## A9.2 — Deterministic provenance

| Behavior | Evidence | Verdict |
|---|---|---|
| Rendering is deterministic for identical inputs | `TestRenderFlowIsDeterministic` (25 renderings compared byte-for-byte) | closed |
| The rendering an invocation used is persisted | `TestA92_DeterministicProvenance` (decodes `rendered_json` and checks the prompt) | closed |
| A later version cannot alter an in-flight invocation | same test (publishes v2 mid-flight, then asserts the persisted prompt) | closed |
| Runs carry the invocation id and their declaration name | same test (store row *and* `GetRun`) | closed |
| Environments carry the invocation id and their declaration name | `TestA93_ReadinessGate`, `TestA95_FailureConvergence` (both read `InvocationEnvs`, which filters on the persisted id) | closed |
| Replay from seq 0 reproduces the same provenance and progress | `TestA92_DeterministicProvenance` (also asserts the replayed log is gapless) | closed |
| A new invocation without a pin uses the newer version | same test | closed |
| Both applications render provenance | `flows.spec.ts "invokes a flow…"`, `flow_e2e.rs renders_flow_invocation_progress` | closed |

## A9.3 — Readiness gate

| Behavior | Evidence | Verdict |
|---|---|---|
| Two declared environments produce exactly two environments | `TestA93_ReadinessGate` | closed |
| No run exists while any required environment is unready | same test (samples every 50ms through provisioning rather than checking once, and asserts `InvokeFlow` itself returns no runs) | closed |
| Agents start once all required environments are ready | same test | closed |
| Each agent's grants name only its declared environments | same test (reads stored grants; asserts `EnvAll` is false and the other environment is not allowed) | closed |
| Delayed readiness is observable as ordered progress | same test (requires `env_ready:*` to precede `stage_started:0`) | closed |
| Setup commands run before agents start | `TestA99_ExampleFlows/environment-backed` (documented key order) | closed |
| Both applications show readiness | `flows.spec.ts`, `flow_e2e.rs renders_flow_invocation_progress` | closed |

## A9.4 — Topology

| Behavior | Evidence | Verdict |
|---|---|---|
| Roots start first and dependents wait | `TestA94_Topology` (samples during execution; fails if a dependent exists while a dependency is non-terminal) | closed |
| Dependents are parented by a declared dependency | same test | closed |
| A shared stage forms one cohort with distinct ordinals | same test (also asserts a single-agent stage forms no cohort) | closed |
| Stage progress is ordered and complete | same test (start precedes complete for every stage) | closed |
| The terminal result is reproducible and typed | same test; `TestA92_DeterministicProvenance` for the reason | closed |
| A spawnable catalog agent can be launched by name with server-supplied definition | `TestA94_AgentRefSpawn` (asserts the child's prompt is the catalog's, not the model's) | closed |
| A non-spawnable agent is refused, uniformly, leaking no name | same test (asserts no run was created and the denial does not contain the referenced name) | closed |
| Both applications render topology | `flows.spec.ts`, `flow_e2e.rs` | closed |

A defect was found and fixed while writing this evidence: `spawn_agent`'s
input required a prompt, so `agent_ref` alone was rejected before resolution
could happen. The prompt is now optional exactly when a resolvable `agent_ref`
supplies one, and a spawn that provides neither is reported to the model
rather than silently creating a promptless run (`loop/spawn.go`).

## A9.5 — Failure convergence

| Behavior | Evidence | Verdict |
|---|---|---|
| One failed and one succeeded environment converges to `failed` | `TestA95_FailureConvergence` (a declared image that cannot exist) | closed |
| The terminal reason is the documented typed value | same test | closed |
| No agent starts | same test | closed |
| Owned environments are released | same test | closed |
| Cleanup is recorded once per resource | same test (progress keys are idempotent, so a repeat would be visible) | closed |
| A session environment the flow does not own is untouched | same test (provisions a bystander first, asserts it is still ready) | closed |
| Declarations are not duplicated by retries | same test (counts environments per declaration; the unique index enforces it) | closed |
| A retry creates a fresh invocation with its own resources | same test (asserts no environment id is shared) | closed |
| Cancelling a terminal invocation changes nothing | same test | closed |
| An unexecutable rendering converges rather than spinning | `TestA95_ValidationAfterLoad` | closed |
| An invocation that cannot make progress converges on an outer deadline and releases its resources | `TestA95_InvocationDeadlineConverges` | closed |

A defect was found and fixed here: an invocation whose persisted rendering
could not be decoded converged without releasing what it owned, because the
cleanup path was reached through the rendering. Ownership is a property of the
rows, so cleanup now runs from them and does not depend on the rendering being
readable (`flowwork/flowwork.go`).

## A9.6 — Cancellation and replay

| Behavior | Evidence | Verdict |
|---|---|---|
| Cancelling during provisioning releases environments and starts nothing | `TestA96_CancelDuringProvisioning` | closed |
| Cancelling during execution cancels the invocation's runs | `TestA96_CancelDuringExecution` (waits for `running` first, so it is execution and not a queued launch) | closed |
| Cancellation is idempotent | `TestA96_CancelDuringProvisioning` (asserts no progress is added by a second cancel) | closed |
| Replay reconstructs ordered progress with no gaps | both tests (assert seq contiguity and key equality against the persisted list) | closed |
| Run terminals appear in the replayed log | `TestA96_CancelDuringExecution` | closed |
| The invocation terminal is not reached before its runs are | same test; the state machine waits for every owned run to be terminal | closed |
| The web application cancels and recovers after reload | `flows.spec.ts "cancels an invocation and recovers state after reload"` | closed |
| The GPUI window cancels and recovers through another replica | `flow_e2e.rs cancels_and_recovers_flow_invocation` | closed |

A defect was found and fixed here: cancellation converged the invocation
immediately, so a replayed log could show an invocation ending before the run
it owned did. The invocation now reaches `cancelled` only once every run it
owns is terminal.

## A9.7 — CLI parity

| Behavior | Evidence | Verdict |
|---|---|---|
| `validate` reports typed errors and exits nonzero | `TestFlowValidateReportsTypedErrors` (both human and JSON modes) | closed |
| A rejected `put` carries the same field paths | same test | closed |
| `put`/`get`/`list`/`versions` match API state | `TestFlowPutGetListRoundTrip` (compares CLI output to a direct API read) | closed |
| An immutable version stays byte-identical through the CLI | same test | closed |
| `invoke --param` renders and runs a real invocation | `TestFlowInvokeStatusAndCancel` | closed |
| `status` reports state, progress, runs, and environments matching the API | same test (compares progress counts to the API) | closed |
| `cancel` converges the invocation | same test | closed |
| A non-completing `--wait` exits nonzero | same test | closed |
| The CLI reaches nothing but the public API | `TestCLIUsesOnlyPublicAPIs` (parses every non-test file's imports) | closed |

## A9.8 — Application parity

| Behavior | Web evidence | GPUI evidence | Verdict |
|---|---|---|---|
| The catalog lists flows with their latest version | `lists the flow catalog and selects a version` | `renders_flow_catalog_and_version_selection` | closed |
| An older version can be selected and shows its own definition | same (asserts the older text is shown and the newer is not) | same (asserts the painted definition marker changes) | closed |
| Structured validation errors are shown and correctable | `rejects an invalid flow with typed field paths` | `renders_flow_validation_errors` | closed |
| A parameterized invocation can be launched | `invokes a flow and shows provenance and progress` | `renders_flow_invocation_progress` | closed |
| Readiness, topology, and provenance are rendered | same | same | closed |
| Linked runs are reachable | same (the run appears in the session run tree) | same (the topology row carries the run id) | closed |
| An invocation can be cancelled from the application | `cancels an invocation and recovers state after reload` | `cancels_and_recovers_flow_invocation` | closed |
| Reconnecting rebuilds the same state | same (reload) | same (second replica) | closed |

Both suites assert against what the application actually painted: the browser
reads rendered `data-*` attributes, and the desktop reads `debug_bounds` on the
real `DesktopWindow`. Neither asserts against a reducer alone, and the desktop
suite drives cancellation through the same window action the rendered control
invokes.

## A9.9 — Executable documentation

| Behavior | Evidence | Verdict |
|---|---|---|
| Every shipped example validates | `TestA99_ExampleFlows` (through `ValidateFlow`, the same surface a user's flow uses) and `TestA99_DocumentedExamplesAreExecuted` | closed |
| Every shipped example completes against the real stack | `TestA99_ExampleFlows` (all three) | closed |
| The documented lifecycle is checked, not just completion | same test (asserts an ordered subsequence of progress keys per example, including provisioning and cleanup for the environment-backed one) | closed |
| Docs and examples cannot drift | `TestA99_DocumentedExamplesAreExecuted` (every shipped file must be documented *and* executed; every executed name must be documented) | closed |

## Cross-cutting

| Behavior | Evidence | Verdict |
|---|---|---|
| Flow event variants cannot be silently skipped by tests | `TestKindCoversEveryEventVariant` (failed until the three new variants were mapped) | closed |
| Generated output cannot drift from the protos | `scripts/verify-codegen.sh`, `verify-codegen-rust.py` in required CI | closed |
| Coverage claims are validated and CI-executed | `python3 scripts/verify-coverage.py`; `TestA79_EvidenceIntegrity` proves the gate rejects false claims | closed |
| Every published flow RPC is claimed by a capability | coverage matrix: the four `A9.8`-deferred flow RPCs and `AgentService/CancelRun` are now claimed | closed |

## Items handed off to Phase 10

Neither of these is a Phase 9 acceptance bullet. Both are real limits found
while auditing, so each is carried into the next phase as a named acceptance
test rather than left as prose.

| Limit | Why it cannot close here | Owner |
|---|---|---|
| Provider capability checks are structural, not behavioral. `flowwork.checkProviders` rejects a readiness policy a provider kind cannot serve, but every kind currently reaches Docker through the loopback proxy, so no test exercises a genuinely incapable provider. | A provider whose control plane really lacks the capability does not exist until the real adapters land. | `A10.10` |
| `GetFlowInvocation` is claimed through `flow_invocation_progress`, whose client tests list a session's invocations and then select one. There is no client path that opens an invocation from its identifier alone. | The applications have no direct invocation route yet; adding one is client work scoped with the Phase 10 application surfaces. | `A10.11` |
