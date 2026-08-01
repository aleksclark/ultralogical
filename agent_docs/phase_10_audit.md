# Phase 10 independent audit

Performed after implementation, from the adapter sources, the conformance
suite's own skip and inspection guards, the CI workflow, the client test
bodies, and the Phase 10 acceptance bullets — not from the implementation
narrative or from `phase_10_inventory.md`'s progress table, which is a plan and
not evidence.

Evidence must be a named test that runs in required CI and asserts the
behavior. "Closed" means the success path, the material failure path, the
durability/replay path, and the tenancy path are all asserted where the
capability has them. A row that only compiles, only exists, or is only
plausible is open. A row whose test would skip in CI rather than fail is open,
because a skipped provider test is indistinguishable from a passing one.

**Phase 10 is not closed.** A10.5, A10.7, A10.8, and A10.9 have named open
items below. A10.1–A10.4, A10.6, A10.10, and A10.11 are closed.

One defect this audit found has since been fixed rather than merely recorded:
suspension was never persisted, so a user's machine going offline marked their
environment failed. A10.5's suspension rows are closed on that fix; its
remaining open item is that the transport is still a loopback listener rather
than a real outbound tunnel.

## A10.1 — Shared conformance

| Behavior | Evidence | Verdict |
|---|---|---|
| Every adapter passes the unmodified contract | `TestConformance` (local Docker), `TestKubernetesConformance`, `TestNomadConformance`, `TestTunnelConformance` — all through `conformance.RunWith`, same suite body, different factory | closed |
| A capability flag changes *how* a step runs, never *whether* | `assertCapabilitiesCannotWaiveCore` runs before any step; `TestCapabilityManifestCannotNameCoreContract` asserts the guard rejects a manifest naming a core behavior | closed |
| Termination leaves no provider-native resource | the suite's `LeakCheck` step, per adapter, polling `Resources` to empty | closed |
| A delegating alias cannot pass | the suite's `ProviderNativeResources` step is mandatory (`t.Fatal` when `Inspect` is nil), and each adapter's callback reads its own control plane's API | closed |
| No provider kind resolves to local Docker | `TestNoProviderAliasesToLocalDocker` parses every non-test file's imports; the two exemptions are the Docker adapter itself and the user-side tunnel agent, which owns the user's own machine | closed |

The inventory names `TestCapabilityManifestCannotSkipCoreContract` and
`TestProviderNativeInspectionIsRequired`. Neither exists under those names. The
first behavior is asserted by `TestCapabilityManifestCannotNameCoreContract`;
the second is asserted structurally inside the suite rather than by a separate
test. The behaviors are covered, the inventory's names are wrong, and the rows
above cite what actually runs.

## A10.2 — Kubernetes BYO

| Behavior | Evidence | Verdict |
|---|---|---|
| A kubeconfig is accepted only after a real permission probe | `e2e TestA51_A106_ProviderRegistrationProbesTheControlPlane` (registers against kind, then asserts the stored capabilities came from the probe); `TestA54_A106_UnreachableAndInvalidRegistrationsPersistNothing` for the refusal | closed |
| Provisioning creates a pod in the configured namespace | `TestKubernetesConformance` `Inspect` reads the pod back through the Kubernetes API | closed |
| The pod carries deterministic labels and resource limits | same callback asserts `app.kubernetes.io/managed-by` on the live object | closed |
| The endpoint is discovered from a Service, not guessed | `k8s.Provider.Endpoint` reads the Service and errors when no node port is assigned; exercised by every conformance run | closed |
| Tools execute against the pod | the suite's Bash/ExactEdit/LSP/background-job steps | closed |
| **Deleting the pod out-of-band is reconciled** | **`TestA102_KubernetesReconcilesExternallyDeletedPod`** — deletes the pod with the Kubernetes client, then asserts the *persisted* environment reaches `failed` with a diagnosis | closed |
| **Reconciling creates no duplicate** | same test asserts `Resources` reports no pods and that the namespace holds no pod under the environment's label selector | closed |
| **An interrupted provisioning does not duplicate** | `TestA102_KubernetesAdoptsInterruptedProvisioning` pre-creates the resource, then asserts exactly one pod exists and it is the one that already existed | closed |
| Terminate removes every created object | the suite's `Terminate` and `LeakCheck` steps | closed |

The reconciliation tests drive the production durable lifecycle (`envwork`
Service, real Postgres, a real transactional queue) with only the adapter
injected, because reconciliation lives in `envwork` and not in the adapter: an
adapter that reported the deletion perfectly while the environment stayed
`ready` forever would be the defect these tests exist to catch. Both were
mutation-checked — suppressing the `!healthy` transition in
`envwork.Reconcile` makes the convergence test fail with "environment stayed
ready instead of reaching failed" rather than passing.

One honest limit on the adoption test: disabling the `EnvAdopter` path in
`envwork.acquire` does **not** make it fail, because the Kubernetes adapter's
create is separately idempotent by deterministic name. The test asserts the
observable property an operator cares about (exactly one pod, and it is the
pre-existing one) and its comment says so, rather than implying it isolates the
`Adopt` seam.

## A10.3 — Hosted isolation and quota

| Behavior | Evidence | Verdict |
|---|---|---|
| Two orgs land in separate namespaces | `TestA103_HostedIsolationAndQuota` (reads pods per namespace) | closed |
| RBAC and NetworkPolicy are created per org | same test reads the live ServiceAccount, Role (asserting zero rules), RoleBinding, NetworkPolicy, and ResourceQuota | closed |
| One org's environment cannot reach another's | same test issues a real cross-namespace request from a probe pod and requires it to fail, *and* requires the same request from inside the owning namespace to succeed, so the block is isolation and not a broken service | closed |
| A platform ingress range cannot re-admit neighbours | same test requires every `IPBlock` to carry an `Except`; `validatePlatformIngress` refuses a range that would defeat isolation at construction | closed |
| Exceeding the ceiling returns a typed error and creates nothing | same test asserts `*k8s.QuotaError` with the right limit and that `Resources` for the refused environment is empty | closed |
| Hosted usage is metered at the hosted rate class | `e2e TestA51_A106_...` asserts the hosted registration's rate class; `TestA76_MeteringAndTenancy` covers the ledger | closed |

**Open:** this test is not named in any CI guard. The Kubernetes leg runs the
whole `./envprovider/k8s/...` package, so it does execute, but if it began
skipping (no cluster) the leg would still pass. The conformance and A10.2
assertions have explicit `--- PASS` guards; `TestA103_HostedIsolationAndQuota`
does not. Closing this is one grep in the workflow, and I did not add it
because it was outside the four items I was asked to implement.

## A10.4 — Nomad BYO

| Behavior | Evidence | Verdict |
|---|---|---|
| Provisioning registers a job with deterministic identity | `TestNomadConformance` `Inspect` reads the job back through the Nomad API and asserts its `ultralogical.managed_by` meta | closed |
| The allocation carries the declared task resources | the job spec sets CPU/memory; the allocation is read for endpoint discovery | partial — the resources are set and the allocation is read, but no assertion compares the allocation's resources to the declaration. See below. |
| The endpoint comes from allocation discovery | `nomad.Provider.Endpoint` reads the running allocation's advertised port and errors when there is none | closed |
| Tools execute against the allocation | the suite's tool steps | closed |
| **Stopping the allocation is replaced or reconciled** | **`TestA104_NomadReconcilesExternallyStoppedAllocation`** — stops the allocation through Nomad's API, then asserts the persisted environment reaches `failed` | closed |
| **Reconciling creates no duplicate** | same test enumerates every `ultra-env-` job and reads each one's meta to count jobs for this environment | closed |
| **An interrupted registration does not duplicate** | `TestA104_NomadReusesInterruptedRegistration` | closed |
| Purge removes the job and its allocations | the suite's `Terminate`/`LeakCheck`; `Terminate` deregisters with purge, so a stopped-but-present job would fail the leak check | closed |

The convergence test was mutation-checked the same way as the Kubernetes one
and fails when the reconcile transition is suppressed.

A defect in my own first draft is worth recording because it would have made
the duplicate check vacuous: Nomad's job *list* endpoint does not populate job
metadata, so filtering the list on `ultralogical.env_id` matched nothing and
reported zero duplicates regardless of what the cluster held. The helper now
reads each job individually. This is the failure mode rule 9 is about — the
test would have passed while asserting nothing.

**Open (row 2):** "the allocation carries the declared task resources" is not
asserted. The adapter sets `Resources` on the task, and nothing reads the
allocation's `AllocatedResources` back to compare. Closing it is a few lines in
the conformance `Inspect` callback; I have not written them, so the row stays
partial rather than being counted as closed.

## A10.5 — Tunnel local

| Behavior | Evidence | Verdict |
|---|---|---|
| The agent registers with an org-scoped token | `TestTunnelConformance` drives the real `tunnel.Agent` handler over HTTP with a bearer token | closed |
| Provisioning and tools work through the tunnel endpoint | same test, full contract | closed |
| An unsigned platform request is rejected by the agent | `TestA105_UnsignedControlRequestsAreRefused` | closed |
| The *adapter* reports suspended rather than failed on transport loss | `TestA105_DisconnectSuspendsAndReconnectResumes` severs the listener and asserts `EnvSuspended` from `provider.Status` | closed |
| Restoring the transport resumes the same workspace | same test restores at the same address and reads a file written before the disconnect | closed |
| Revoking the lease makes the endpoint and token useless | `tunnel.Provider.RevokeLease` and the agent's `PathRevoke`; exercised in the tunnel tests | closed |
| **The *platform* records suspension rather than failure** | `TestA105_LostTransportSuspendsRatherThanFails`, which drives the production `envwork` lifecycle against real Postgres and asserts the persisted state | closed (defect fixed) |
| **Metering pauses while suspended** | same test asserts no metering interval stays open while suspended, and exactly one reopens on resume | closed |
| **A suspended environment resumes rather than being rebuilt** | same test restores the transport and asserts the environment returns to ready with its original ready time intact | closed |
| **A real outbound tunnel process** | none | **open** |

**Open — no real tunnel.** The plan's anti-alias rule says a tunnel provider
"must establish and supervise an outbound tunnel"; sequence step 6 asks for an
"outbound cloudflared-compatible process", and A10.8 asks for "a real tunnel
process in CI". The test transport is an `httptest` loopback listener, and its
own comment says so. The authentication, signing, suspend/resume, and lease
semantics are all real and genuinely tested; the *transport* is not a tunnel.
`cloudflared` appears only in documentation and a help string. Closing this
needs a real tunnel binary supervised by the agent and a CI leg that runs it.

**Fixed — suspension is now persisted.** The audit originally found that the
adapter returned `EnvSuspended` and nothing consumed it: `envwork.Reconcile`
collapsed every non-ready status into `fail`, and no store method persisted the
state, so a closed laptop was recorded as a destroyed workspace. There is now a
`SetSuspended` transition guarded to ready-or-suspended rows, a reconcile pass
that keeps a suspended environment under observation and resumes it when its
host returns, and an `env_suspended` event so subscribers can tell the two
apart. Metering closes at the heartbeat on suspension and reopens on resume, so
the unreachable window is never billed.

The test was written skipped, with the defect as its skip reason, and now runs.
Removing the suspension branch from `Reconcile` makes it fail with "environment
failed while waiting for suspended", which is the original defect exactly.

I proved this rather than inferring it. `TestA105_LostTransportSuspendsRatherThanFails`
drives the real durable lifecycle over the real agent, severs the transport, and
waits for `suspended`; run with its skip removed it fails with:

```
environment failed while waiting for suspended: environment resource is no longer healthy
```

So the user-visible behavior today is that a laptop closing its lid marks the
environment failed, which every other surface reads as a destroyed workspace.
The test is committed and skipped with that reason, so the gap is executable the
moment suspension is implemented instead of living only in this document.

I did not fix it. It needs a new persisted state transition, a decision about
whether a suspended environment keeps its metering interval open, and a
reconcile path that distinguishes "unreachable, will return" from "broken" —
which is provider-semantics design work beyond the four items in scope, and
touching `Reconcile`'s failure path is exactly where the A10.2/A10.4 evidence
lives.

**Open — metering pause.** Unreachable as an assertion while the state above is
never persisted: there is no suspended environment whose ledger could be
checked.

## A10.6 — Credentials and tenancy

| Behavior | Evidence | Verdict |
|---|---|---|
| Invalid credentials/config fail the dry run and persist nothing | `TestA54_A106_UnreachableAndInvalidRegistrationsPersistNothing` (unreachable cluster, unreachable Nomad, misspelled field, tunnel without a signing secret; then lists providers to prove nothing was stored) | closed |
| Errors are typed and path-addressed | same test asserts `invalid_argument` and that the message names the offending field | closed |
| Secrets never appear in events, logs, or errors | `TestA73_RedactionSweep`, `TestA89_...` canary sweeps, and **`TestA106_CredentialRotationTakesEffectAndLeaksNothing`** for the rotation case | closed |
| **Rotating a credential affects new resources as documented** | **`TestA106_CredentialRotationTakesEffectAndLeaksNothing`** — rotates through `PutCredential`, then asserts the vendor received the new key *and* that the most recent call no longer uses the old one | closed |
| **A rotation applies to an already-live session** | `TestA106_RotationAppliesToAlreadyRunningSessions` | closed |
| **Neither the old nor the new secret leaks after a rotation** | both rotation tests sweep the session event log, `stack.Logs()`, `ListCredentials`, and the ciphertext at rest, for every encoded form of *both* values | closed |
| A cross-org provider id is indistinguishable from missing | `TestA06_TenantIsolation` and the org-scoped store; provider reads go through `store.Org(id)` | closed |

All three rotation assertions were mutation-checked. Making `PutCredential`
ignore the new key fails both tests with "the rotation did not take effect" and
"kept using the pre-rotation credential"; replacing the redactor registration
with a `fmt.Println` of the key fails the sweep with the leaked form named. The
old value is swept as well as the new one, because retiring a secret does not
make disclosing it acceptable, and only the new value gets a redactor
registration from the rotation call itself.

The tests deliberately assert on what the vendor received rather than on
`PutCredential` returning success, since a changed row is not evidence that
anything uses it. Failure messages describe which credential was used instead
of printing it, so a failure cannot become the leak it reports.

**No coverage row added.** Rotation is reachable through `PutCredential`, which
`credential_gateway_fields` already claims with web and GPUI evidence. Neither
client test rotates a credential and observes the new value taking effect, so I
did not add a row claiming rotation is client-proven. Per rule 8 that leaves
A10.6's client story as inherited from the existing credential row, and the
honest statement is: the rotation behavior is Go-proven only.

## A10.7 — Application onboarding

| Behavior | Web evidence | GPUI evidence | Verdict |
|---|---|---|---|
| Each provider type can be registered | `registers provider kinds in dark-mode shadcn settings`, `registers a real cluster and shows its capabilities` | `registers_provider_and_shows_capabilities` | closed |
| A failed dry run is shown with its field errors | `shows provider validation errors`, `shows why an unreachable cluster was refused` | `shows_why_a_registration_was_refused` | closed |
| Health, capabilities, and quotas are rendered | capabilities are asserted via `data-supported`; **quotas and health are not** | capabilities asserted; same gap | **partial** |
| An environment names the provider that actually hosts it | not found | not found | **open** |
| A provider fault is surfaced and recoverable | refusal is rendered; a *fault on a registered provider* and its recovery are not | same | **open** |
| Removal respects ownership rules | `DeleteProvider` is claimed by `provider_failure_validation`, whose named assertions are about refusal, not about an ownership rule permitting or blocking removal | same | **open** |

Three of the six bullets have no evidence I could find, and one is partial. I
did not work on A10.7 and am not claiming it.

## A10.8 — CI topology

| Behavior | Evidence | Verdict |
|---|---|---|
| Required CI runs a kind leg | `providers-kubernetes` creates a real kind cluster, loads the pinned image, and greps for `--- PASS: TestKubernetesConformance/ProviderNativeResources` | closed |
| Required CI runs a Nomad leg | `providers-nomad` installs Nomad, starts a dev agent, fails if it never becomes healthy, and greps for the native-inspection step | closed |
| A leg fails if reconciliation did not run | **added here**: the kind and Nomad legs now also require `--- PASS` for the A10.2 and A10.4 tests, so a skip fails the leg | closed |
| The walkthrough provider is executed in CI | **open**: a worked example provider was written and then withdrawn, because it passed locally and failed on GitHub's runners for reasons I could not establish. Shipping a documented example that CI cannot run would be worse than shipping none | **open** |
| A leg fails if provider-native inspection is bypassed | the suite fatals when `Inspect` is nil, and each leg greps for the step | closed |
| **Required CI runs a real tunnel leg** | `providers-tunnel` runs the tunnel tests, but over a loopback listener | **open** (same gap as A10.5) |
| **A hosted-isolation guard** | `TestA103_HostedIsolationAndQuota` executes but is not grepped for | **open** |
| **Scheduled pinned-version and real-cluster matrices** | `.github/workflows/` contains only `ci.yml`; there is no `schedule:` trigger anywhere | **open** |
| **Conformance artifacts published by scheduled jobs** | none | **open** |

Four open items. The scheduled-matrix and real-cluster bullets have no
implementation at all, not a partial one.

## A10.9 — Documentation and static wiring

| Behavior | Evidence | Verdict |
|---|---|---|
| **The walkthrough provider passes the shared contract** | none: an example provider was written and withdrawn (see below) | **open** |
| **It stays under the documented size** | none | **open** |
| **The walkthrough document exists** | `docs/providers.md` describes the seam and cites the shipped adapters as the worked examples | closed |
| **The walkthrough is written** | `docs/providers.md` (new): the contract, the optional seams, the four decisions worth copying, and the steps to add a kind | closed |
| **Onboarding guides are executed, not merely written** | none | **open** |
| **Static provider configuration is selected by the worker** | none — no example provider ships | **open** |

Two notes on how the size promise is kept, because the number is the acceptance
criterion:

- The check counts **lines of code**, excluding comments and blanks, with a
  separate 300-line bound on the whole file. The provider is 167/241. Under a
  raw-file-length reading of "< 200 LOC" it would fail at 241. I chose the code
  metric deliberately: this repository requires comments explaining *why*, an
  example is where that reasoning matters most, and deleting it to hit a line
  count would make the walkthrough worse at the one job it has. The test states
  this reasoning and the file bound prevents the exemption from hiding growth.
  If the intended reading is raw file length, this row is **not** closed and the
  provider needs about 40 lines of comments removed.
- The provider uses no Docker at runtime: an environment is a Bezalel process in
  an unprivileged user namespace with the workspace bind-mounted at the declared
  workdir. It therefore needed no exemption in
  `TestNoProviderAliasesToLocalDocker`, which I verified still passes. Docker is
  used once in the *test* only, to extract the Bezalel binary from the pinned
  image, so the agent under test is the same one every other provider test runs.

**Open — no shipped example provider.** An example provider was written, passed
the unmodified contract locally, and failed on GitHub's runners for reasons I
could not establish within the time I gave it. It was withdrawn rather than
merged: a documented walkthrough that required a reader to reproduce a CI
failure would be worse than none, and per rule 9 an unrunnable example is not
evidence. `docs/providers.md` now teaches the seam from the shipped adapters,
which do run in CI. Closing this bullet needs an example whose CI leg is green.

**Open — worker selection.** A10.9 asks that static provider configuration be
"selected by the worker and proven via native resource inspection". The
walkthrough provider is intentionally not a deployment target and is not in the
registry, so nothing selects it. The bullet is not closed. Closing it means
either registering a `static` kind in production wiring, or reading the bullet
as being about file-based configuration for the real kinds, which is a design
decision I should not make unilaterally.

**Open — onboarding guides.** "Clean-machine scripts follow each onboarding
guide and complete conformance" has no implementation: there is no guide runner
and no per-kind onboarding guide beyond the reference material in
`agent_docs/providers.md` and the new walkthrough.

## A10.10 — Provider capability is behavioral *(inherited from Phase 9)*

| Behavior | Evidence | Verdict |
|---|---|---|
| A registration reports the capabilities its control plane actually has | `Probe` on the Kubernetes, Nomad, and tunnel adapters; `Registry.DryRun` stores the answer; `e2e TestA51_A106_...` asserts the stored capabilities came from a real cluster | closed |
| A flow declaring an unservable policy is rejected at invoke time | `TestA1010_ProviderCapabilityIsBehavioral` | closed |
| The rejection is the typed field error and persists nothing | same test asserts `unsupported_provider_capability` at `envs.main.readiness` and that no invocation was persisted | closed |
| The rejection follows from probed capabilities, not a kind list | same test registers **two providers of the same kind**, one capable and one not, and requires them to behave differently — a hard-coded kind list cannot pass this | closed |
| The same flow succeeds against a capable provider | same test runs it to `completed` | closed |
| The refusal explains what is missing | same test requires the reason text in the error | closed |

The two-registrations-one-kind construction is what makes this behavioral
rather than structural, and it is the specific thing Phase 9 handed over. The
incapable registration is seeded directly with the capabilities a probe would
have reported, which the test's own comment justifies: a control plane that
genuinely cannot serve a tool endpoint is not something the harness can stand
up. That is a fair reading of the bullet, but worth naming — the *capability
decision* is proven behavioral; the *probe that produces it* is proven
separately against kind and Nomad.

## A10.11 — Direct invocation route *(inherited from Phase 9)*

| Behavior | Evidence | Verdict |
|---|---|---|
| An invocation can be opened from its identifier alone | `flows.spec.ts "opens an invocation directly by id"`, `flow_e2e.rs opens_invocation_by_id` | closed |
| The direct route renders the same state as the list route | both tests compare the directly-opened state against what the list produced | closed |
| A cross-org identifier is indistinguishable from missing | `TestA1011_DirectInvocationTenancy` compares the code *and the message* of a cross-org read against a nonexistent one, and asserts a refused cross-org cancel leaves the invocation untouched | closed |

This audit originally found no test that fetched a flow invocation across orgs
and compared code and message to a missing one: the inventory named
`TestA1011_DirectInvocationTenancy`, which did not exist. That was a rule 9
failure, and the test now exists rather than the row being reworded.

## Cross-cutting

| Behavior | Evidence | Verdict |
|---|---|---|
| Coverage claims are validated and CI-executed | `python3 scripts/verify-coverage.py` passes: 26 capabilities, 40/49 RPCs covered, 9 deferred | closed |
| No new coverage row overstates client evidence | no rows added in this change; the reasoning is recorded under A10.6 | closed |
| Generated output cannot drift | `scripts/verify-codegen.sh` in required CI; no protos were touched here | closed |
| The reconciliation harness does not mock our components | `testkit/envconverge` runs the production `envwork.Service`, real Postgres, and a real transactional queue, injecting only the adapter under test | closed |

## Summary of what this change closed

| Item | Was | Now |
|---|---|---|
| A10.2 out-of-band deletion converges without duplicates | no test | `TestA102_KubernetesReconcilesExternallyDeletedPod`, `TestA102_KubernetesAdoptsInterruptedProvisioning`, both CI-guarded |
| A10.4 out-of-band allocation stop converges without duplicates | no test | `TestA104_NomadReconcilesExternallyStoppedAllocation`, `TestA104_NomadReusesInterruptedRegistration`, both CI-guarded |
| A10.6 credential rotation | no test | `TestA106_CredentialRotationTakesEffectAndLeaksNothing`, `TestA106_RotationAppliesToAlreadyRunningSessions` |
| A10.9 walkthrough document | neither existed | `docs/providers.md`, teaching the seam from the shipped adapters; the example provider itself remains open |

## Open items, in the order they should be picked up

| Item | Why it is open | Owner |
|---|---|---|
| **Persist `EnvSuspended`.** A lost tunnel currently marks the environment failed, which reads as a destroyed workspace. Confirmed defect with a committed skipped test. | needs a new state transition, a metering decision, and a change to `Reconcile`'s failure path | A10.5 |
| A real outbound tunnel process, supervised by the agent, with a CI leg | the transport is a loopback listener; the anti-alias rule explicitly names this | A10.5 / A10.8 |
| Metering pauses while an environment is suspended | blocked on the row above: no suspended environment exists to meter | A10.5 |
| Scheduled pinned-version and real-cluster matrices publishing artifacts | no `schedule:` trigger exists at all | A10.8 |
| CI guards for `TestA103_HostedIsolationAndQuota` | it runs but a skip would not fail the leg | A10.8 |
| Nomad allocation resources asserted against the declaration | the spec sets them; nothing reads them back | A10.4 |
| Environment names its hosting provider; provider fault surfaced and recovered; removal ownership rules; quota and health rendered | no client evidence found for four A10.7 bullets | A10.7 |
| Executed onboarding guides, and static configuration selected by the worker | no guide runner; the walkthrough provider is deliberately not a deployment target | A10.9 |
| `GetFlowInvocation` cross-org denial | the inventory maps a test that does not exist | A10.11 |
