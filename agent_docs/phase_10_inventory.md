# Phase 10 inventory — Phase 5 bullet to bounded behavior to named test

Written before production changes, per Phase 10 required sequence step 1. Every
open Phase 5 audit bullet, plus the two limits handed over from Phase 9, is
decomposed into bounded observable behaviors, the production entrypoint that
exposes each, and the named test that asserts it. Behavior that does not exist
yet is marked **unimplemented before Phase 10** so the phase cannot close by
renaming partial work.

Legend: `go:` real-stack Go test, `conf:` shared provider conformance run,
`cli:` `cmd/ultra` test, `web:` Playwright against the dark shadcn application,
`gpui:` Rust test driving the rendered GPUI window.

## Progress

Phase 10 is **in progress and not closeable**. The independent completion audit
is in [phase_10_audit.md](phase_10_audit.md); it is the authoritative statement
of what is closed, and this table is a summary of it rather than a separate
claim.

| Area | State |
|---|---|
| Capability manifest, conformance parameterization, native-inspection requirement | done (A10.1) |
| Real Kubernetes adapter passing the unmodified contract on kind | done (A10.1) |
| Kubernetes convergence after out-of-band deletion, with no duplicate | done (A10.2) |
| Hosted namespace/RBAC/NetworkPolicy/quota isolation, proven by real cross-namespace traffic | done (A10.3), but no CI guard against it skipping |
| Real Nomad adapter passing the unmodified contract on a dev agent | done (A10.1) |
| Nomad convergence after an out-of-band allocation stop, with no duplicate | done (A10.4) |
| Nomad allocation resources asserted against the declaration | **open** (A10.4) |
| Tunnel agent control API, request signing, reconnect, lease revocation | done (A10.1); the transport is still a loopback listener rather than a real outbound tunnel |
| Durable suspension: a lost host suspends rather than fails, pauses metering, and resumes | done (A10.5) |
| Tunnel suspension persisted by the platform | **open, confirmed defect** (A10.5) |
| A real outbound tunnel process | **unimplemented** (A10.5, A10.8) |
| Provider registry, per-registration adapters, registration dry run; loopback alias deleted | done (A10.6) |
| Credential rotation takes effect and leaks neither old nor new value | done (A10.6), Go-proven only |
| Provider registration and refusal surfaces in both applications | done (A10.7) |
| Environment names its host, provider fault recovery, removal ownership, quota/health rendering | **open** (A10.7) |
| kind, Nomad, tunnel, and static CI legs with native-inspection guards | done (A10.8) |
| Scheduled pinned-version and real-cluster matrices | **unimplemented** (A10.8) |
| Static walkthrough provider passing the unmodified contract, under the documented size | done (A10.9) |
| `docs/providers.md` walkthrough | done (A10.9) |
| Executed onboarding guides; static configuration selected by the worker | **unimplemented** (A10.9) |
| Capability decided behaviorally rather than by kind | done (A10.10) |
| Direct invocation route in both applications | done (A10.11) |
| `GetFlowInvocation` cross-org denial | **open** — the row below maps a test that does not exist (A10.11) |

Three corrections to this document's own rows, found while auditing. The A10.1
tests named `TestCapabilityManifestCannotSkipCoreContract` and
`TestProviderNativeInspectionIsRequired` never existed under those names; the
behaviors are covered by `TestCapabilityManifestCannotNameCoreContract` and by a
mandatory step inside the conformance suite, and the rows now cite those.
`TestA1011_DirectInvocationTenancy` did not exist at all, so it was written
rather than the row being quietly reworded. Naming a test that does not exist is
the failure rule 9 describes, and it is recorded here rather than erased.

## Reconciliation: what Phase 5 actually shipped

`envprovider/proxy` registers one adapter for `byo_k8s`, `hosted_eks`,
`byo_nomad`, and `tunnel_local`. In `loopback` mode every one of them calls
`localdocker` for `Provision`, `Status`, `Endpoint`, `Restart`, and
`Terminate`; in `remote` mode every one returns "remote provider control plane
not connected". `cmd/ultra-env-agent` serves a single authenticated `/health`
route and provisions nothing.

So the honest starting position is: **four provider kinds exist as names, and
none of them has an implementation.** Everything below is new work except where
a row says otherwise.

| Phase 5 bullet | State entering Phase 10 |
|---|---|
| `envprovider/k8s` pod-per-env | **unimplemented**; `byo_k8s` is a Docker alias |
| `envprovider/nomad` job-per-env | **unimplemented**; `byo_nomad` is a Docker alias |
| `envprovider/tunnel` + real env agent | **unimplemented**; the agent has no control API |
| Hosted EKS namespaces, quotas, NetworkPolicy | **unimplemented**; only the `hosted` rate class is real |
| Credential intake with dry-run validation | partial: JSON shape and a `/health` GET; no control-plane probe |
| Capability flags | **unimplemented**; conformance is uniform |
| Provider registry/factory | **unimplemented**; kinds are wired by hand in `cmd/worker` |
| kind / Nomad / tunnel CI legs | **unimplemented** |
| Provider onboarding surfaces | partial: registration form only |
| `docs/providers.md` walkthrough | **unimplemented** |

## A10.1 — Shared conformance

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Every adapter passes the unmodified contract | `envprovider/conformance.Run` | `conf: TestKubernetesConformance`, `TestNomadConformance`, `TestTunnelConformance`, `TestLocalDockerConformance` |
| A capability flag changes *how* a step runs, never *whether* | capability manifest | `go: TestCapabilityManifestCannotNameCoreContract` |
| A skipped step names a declared capability and a reason | conformance skip guard | same test |
| Termination leaves no provider-native resource | `EnvResourceLister` per adapter | asserted inside each conformance run |

## A10.2 — Kubernetes BYO

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| A kubeconfig is accepted only after a real permission probe | `envprovider/k8s` dry run (**unimplemented**) | `go: e2e TestA102_KubernetesBYO` |
| Provisioning creates a pod in the configured namespace | `k8s.Provider.Provision` | same test, via a Kubernetes client read |
| The pod carries deterministic labels, owner identity, and resource limits | pod spec | same test (reads the live object, not the request) |
| The endpoint is discovered from a Service, not guessed | `k8s.Provider.Endpoint` | same test |
| Tools execute against the pod | MCP over the discovered endpoint | same test |
| Deleting the pod out-of-band is reconciled | `envwork` reconcile + adapter status | same test |
| Terminate removes every created object | `k8s.Provider.Terminate` + `Resources` | same test |

## A10.3 — Hosted isolation and quota

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Two orgs land in separate namespaces | hosted namespace policy (**unimplemented**) | `go: e2e TestA103_HostedIsolationAndQuota` |
| RBAC and NetworkPolicy are created per org | hosted policy objects | same test (reads the live objects) |
| One org's environment cannot reach another's | NetworkPolicy | same test (a real cross-namespace request must fail) |
| Exceeding the concurrent-environment ceiling returns a typed error and creates nothing | quota check at provision | same test |
| Hosted usage is metered at the hosted rate class | `UsageStore` rate class | same test |

## A10.4 — Nomad BYO

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Provisioning registers a job with deterministic identity | `envprovider/nomad` (**unimplemented**) | `go: e2e TestA104_NomadBYO` |
| The allocation carries the declared task resources | job spec | same test (reads the allocation) |
| The endpoint comes from allocation discovery | `nomad.Provider.Endpoint` | same test |
| Tools execute against the allocation | MCP | same test |
| Stopping the allocation is replaced or reconciled | adapter status + `envwork` | same test |
| Purge removes the job and its allocations | `Terminate` + `Resources` | same test |

## A10.5 — Tunnel local

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| The agent registers with an org-scoped token | `cmd/ultra-env-agent` (**control API unimplemented**) | `go: e2e TestA105_TunnelLocal` |
| Provisioning and tools work through the tunnel endpoint | `envprovider/tunnel` | same test |
| An unsigned platform request is rejected by the agent | agent request-signature check | same test |
| Losing the transport suspends rather than fails the environment | tunnel disconnect handling | same test |
| Metering pauses while suspended | `UsageStore` | same test |
| Restoring the transport resumes the same workspace | reconnect handling | same test |
| Revoking the lease makes the endpoint and token useless | lease revocation | same test |

## A10.6 — Credentials and tenancy

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Invalid credentials fail the dry run and persist nothing | `RegisterProvider` dry run | `go: e2e TestA106_CredentialsAndTenancy` |
| Errors are typed and path-addressed | provider config validation | same test |
| Secrets never appear in events, logs, or errors | `secrets.Redactor` | same test (canary sweep) |
| Rotating a credential affects new and reconciled resources as documented | credential store + adapters | same test |
| A cross-org provider id is indistinguishable from missing | org-scoped store | same test |

## A10.7 — Application onboarding

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Each provider type can be registered from the applications | provider settings surfaces | `web: providers.spec.ts`, `gpui: provider_e2e.rs` |
| A failed dry run is shown with its field errors | registration error rendering | same |
| Health, capabilities, and quotas are rendered | provider status surface (**unimplemented**) | same |
| An environment names the provider that actually hosts it | env detail surface | same |
| A provider fault is surfaced and recoverable | provider health | same |
| Removal respects ownership rules | delete guard | same |

## A10.8 — CI topology

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| Required CI runs kind, Nomad, and tunnel legs | `.github/workflows/ci.yml` (**unimplemented**) | CI job definitions |
| A leg fails if the adapter did not create provider-native resources | native inspection inside each conformance run | asserted structurally: `conformance.RunWith` fails when `Inspect` is nil, and each CI leg greps for its `ProviderNativeResources` pass |
| Scheduled jobs cover pinned versions and publish artifacts | scheduled workflow | workflow definition |

## A10.9 — Documentation and static wiring

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| The stub-provider walkthrough produces a passing provider under the documented size | `docs/providers.md` + `envprovider/static` (**unimplemented**) | `go: TestA109_StaticProviderWalkthrough` |
| Onboarding guides are executed, not merely written | scripted guide runner | `go: e2e TestA109_OnboardingGuides` |
| Static provider configuration is selected by the worker | worker provider wiring | same test, proven by native inspection |

## A10.10 — Provider capability is behavioral *(inherited from Phase 9)*

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| A registration reports the capabilities its control plane actually has | capability probe at registration (**unimplemented**) | `go: e2e TestA1010_ProviderCapabilityIsBehavioral` |
| A flow declaring a policy the provider cannot serve is rejected at invoke time | `flowwork.checkProviders` reading probed capabilities, not a kind list | same test |
| The rejection is the existing typed field error and persists nothing | `unsupported_provider_capability` | same test |
| The same flow succeeds against a capable provider | unchanged invoke path | same test |

## A10.11 — Direct invocation route *(inherited from Phase 9)*

| Bounded behavior | Production entrypoint | Named test |
|---|---|---|
| An invocation can be opened from its identifier alone | direct route in both applications (**unimplemented**) | `web: flows.spec.ts "opens an invocation directly by id"`, `gpui: flow_e2e.rs opens_invocation_by_id` |
| The direct route renders the same state as the list route | shared invocation view | same tests |
| A cross-org identifier is indistinguishable from missing | `GetFlowInvocation` denial | `go: e2e TestA1011_DirectInvocationTenancy` |
