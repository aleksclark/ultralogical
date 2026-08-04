# Extraction inventory

Authoritative keep / generalize / drop / move-to-consumer map for the core
extraction. Built by walking the tree at commit `16629b5` (Phase 10 merge
baseline). Every row has a disposition and the phase that executes it. Zero
TBD.

Disposition key:

| Tag | Meaning |
|---|---|
| **keep** | Survives into the extracted core (may be renamed later) |
| **generalize** | Kept, but env-named surface becomes resource-generic (E2) |
| **drop** | Deleted from the core; not relocated |
| **move-to-consumer** | Behavior leaves the core; a named consumer owns it |

Phase key: E1 shed product surface · E2 resource generalization · E3 tenancy /
identity / policy · E4 API v1 + squash · E5/E6 consumer proofs (no deletions).

---

## 1. Root domain types

Source files walked: `ultra.go`, `run.go`, `env.go`, `event.go`, `flow.go`,
`flowdef.go`, `multiplayer.go`, `automation.go`, `credential.go`,
`capability.go`, `auth.go`, `store.go`, `secrets/*`.

### 1.1 `ultra.go` — tenancy and sessions

| Symbol | Disposition | Phase | Notes |
|---|---|---|---|
| `OrgID` | keep (rename → `TenantID`) | E3 | Structural tenancy id |
| `UserID` | drop | E3 | Replaced by tenant API keys |
| `SessionID` | keep | — | Core session identity |
| `OrgRole` (+ `OrgRoleOwner/Admin/Member`) | drop | E3 | Human role model |
| `Org` | keep (rename → `Tenant`) | E3 | Drop `Plan` field in E1 first |
| `Org.Plan` | drop | E1 | Billing plan string |
| `User` | drop | E3 | Human identity |
| `OrgMember` | drop | E3 | Human membership |
| `Session` | keep | E3 adds `Labels` | Durable unit of work |

### 1.2 `run.go` — agent runs

| Symbol | Disposition | Phase | Notes |
|---|---|---|---|
| `RunID` | keep | — | |
| `RunState` (+ `RunPending` / `RunRunning` / `RunAwaiting` / `RunCompleted` / `RunFailed` / `RunCancelled`, `Terminal`) | keep | — | |
| `FailureCredentialMissing` / `FailureCredentialInvalid` / `FailureProviderError` / `FailureInternal` | keep | — | Typed failure reasons |
| `ModelConfig` | keep | — | BYO inference config |
| `AgentRun` | keep | E1 strips flow fields | Core run envelope |
| `AgentRun.FlowInvocationID` | drop | E1 | Flow provenance |
| `AgentRun.FlowAgentName` | drop | E1 | Flow provenance |
| `AgentRun.Grants` | generalize | E1→E3 | Lattice → flat `Tools []string` in E1; policy hook in E3 |
| `AgentRun.ParentRunID` / `SpawnKey` / `Cohort*` | keep | — | Run trees + orchestration |
| `RunStep` | keep | — | |
| `RunStore` | keep | E1 drops flow-aware methods if any | |

### 1.3 `env.go` — environments / providers / metering

| Symbol | Disposition | Phase | Notes |
|---|---|---|---|
| `EnvID` | generalize → `ResourceID` | E2 | |
| `ProviderInstanceID` | keep | — | |
| `EnvState` (+ `EnvRequested` / `EnvProvisioning` / `EnvReady` / `EnvSuspended` / `EnvTerminating` / `EnvTerminated` / `EnvFailed`, `Terminal`) | generalize → `ResourceState` | E2 | Lifecycle unchanged |
| `EnvSpec` | generalize (raw JSON behind kind) | E2 | Kind-specific schema moves into provider |
| `ProviderHandle` | generalize | E2 | |
| `ProviderStatus` | keep / generalize | E2 | |
| `EnvProvider` | generalize → `ResourceProvider` | E2 | |
| `EnvAdopter` | generalize → `ResourceAdopter` | E2 | |
| `EnvResourceLister` | generalize → `ResourceLister` | E2 | |
| `DevEnv` | generalize → `Resource` | E2 | First kind remains `dev_env` |
| `DevEnv.FlowInvocationID` / `FlowEnvName` | drop | E1 | Flow provenance on env rows |
| `ProviderInstance` | keep | E2 adds `Kind` emphasis | Tenant-scoped registration |
| `ProviderKindLocalDocker` | keep | — | |
| `ProviderKindBYOKubernetes` | keep | — | |
| `ProviderKindHostedEKS` | drop | E1 | Hosted isolation product |
| `ProviderKindBYONomad` | keep | — | |
| `ProviderKindTunnelLocal` | keep | — | |
| `ProviderKindStatic` | keep | — | |
| `RateClassBYO` / `RateClassHosted` | drop | E1 | Metering rate classes |
| `EnvUsage` | drop | E1 | Metering ledger row |
| `EnvStore` | generalize → `ResourceStore` | E2 | |
| `ProviderInstanceStore` | keep | — | |
| `UsageStore` | drop | E1 | Metering API |

### 1.4 `event.go` — event log vocabulary

| Symbol | Disposition | Phase | Notes |
|---|---|---|---|
| `ActorType` (+ `ActorUser` / `ActorAgent` / `ActorSystem`) | keep (opaque attribution) | E3 reshapes who fills it | Core log attribution; human-vs-agent values may collapse later |
| `Actor` | keep (opaque attribution) | E3 reshapes who fills it | Core log attribution |
| `Event` | keep | — | Gapless per-session seq |
| `EventBus` | keep | — | |
| `EventKindUserMessage` | keep | — | |
| `EventKindAnnotation` | keep | — | |
| `EventKindRunStarted` | keep | — | |
| `EventKindStepStarted` | keep | — | |
| `EventKindTextDelta` | keep | — | |
| `EventKindReasoningDelta` | keep | — | |
| `EventKindToolCallStart` | keep | — | |
| `EventKindToolResult` | keep | — | |
| `EventKindStepFinished` | keep | — | |
| `EventKindRunAwaiting` | keep | — | `ask_user` awaiting state |
| `EventKindRunCompleted` | keep | — | |
| `EventKindRunFailed` | keep | — | |
| `EventKindRunCancelled` | keep | — | |
| `EventKindEnvRequested` | generalize → resource_* | E2 | |
| `EventKindEnvProvisioning` | generalize | E2 | |
| `EventKindEnvReady` | generalize | E2 | |
| `EventKindEnvFailed` | generalize | E2 | |
| `EventKindEnvSuspended` | generalize | E2 | |
| `EventKindEnvTerminating` | generalize | E2 | |
| `EventKindEnvTerminated` | generalize | E2 | |
| `EventKindExecPreviewRan` | generalize / keep as resource tool preview | E2 | |
| `EventKindParticipantJoined` | drop | E1 | Human presence |
| `EventKindParticipantLeft` | drop | E1 | Human presence |
| `EventKindParticipantIdle` | drop | E1 | Human presence |
| `EventKindRunSpawned` | keep | — | Run trees |
| `EventKindMemorySet` | keep | — | Session scratchpad |
| `EventKindMemoryDeleted` | keep | — | |
| `EventKindPermissionDenied` | keep | — | Uniform denial stub |
| `EventKindHistoryCompacted` | keep | — | Loop concern |
| `EventKindModelFallback` | keep | — | Loop concern |
| `EventKindHookFired` | keep | — | Automation hook visibility |
| `EventKindPeriodicPromptFired` | keep | — | Periodic prompts stay |
| `EventKindFlowInvoked` | drop / move-to-consumer | E1 | Flows → curri |
| `EventKindFlowProgressed` | drop / move-to-consumer | E1 | |
| `EventKindFlowTerminal` | drop / move-to-consumer | E1 | |
| `UserMessagePayload` | keep | — | |
| `AnnotationPayload` | keep | — | |
| `RunStartedPayload` | keep | — | |
| `StepStartedPayload` | keep | — | |
| `TextDeltaPayload` | keep | — | |
| `ReasoningDeltaPayload` | keep | — | |
| `ToolCallStartedPayload` | keep | — | |
| `ToolResultPayload` | keep | — | |
| `StepFinishedPayload` | keep | — | |
| `Question` | keep | — | `ask_user` payload piece |
| `RunAwaitingPayload` | keep | — | UI affordance is consumer-side |
| `RunCompletedPayload` | keep | — | |
| `RunFailedPayload` | keep | — | |
| `RunCancelledPayload` | keep | — | |
| `EnvEventPayload` | generalize | E2 | |
| `ExecPreviewRanPayload` | generalize | E2 | |
| `ParticipantEventPayload` | drop | E1 | |
| `RunSpawnedPayload` | keep | — | |
| `MemoryEventPayload` / `NewMemoryEventPayload` | keep | — | |
| `HistoryCompactedPayload` | keep | — | |
| `ModelFallbackPayload` | keep | — | |
| `HookFiredPayload` | keep | — | |
| `PeriodicPromptFiredPayload` | keep | — | |
| `PermissionDeniedPayload` | keep | — | |
| `FlowInvokedPayload` | drop | E1 | |
| `FlowProgressedPayload` | drop | E1 | |
| `FlowTerminalPayload` | drop | E1 | |

### 1.5 `flow.go` / `flowdef.go` — versioned flow catalog

| Symbol | Disposition | Phase | Notes |
|---|---|---|---|
| Entire `flow.go` (`FlowID`, `Flow`, `FlowInvocation*`, `FlowStore`, …) | drop / move-to-consumer | E1 | Consumer: curri-agents Flow model |
| Entire `flowdef.go` (`FlowDefinition`, `ValidateFlowDefinition`, `RenderFlow`, …) | drop / move-to-consumer | E1 | flowdef language leaves with flows |
| `flowdef_test.go` | drop | E1 | |

### 1.6 `multiplayer.go` — grants, presence, memory, waits

| Symbol | Disposition | Phase | Notes |
|---|---|---|---|
| `Grants` lattice fields (`EnvAll`, `Envs`, `MaySpawn`, `MaxChildren`) | drop | E1 | Over-general privilege lattice |
| `Grants.Tools` / `AllowsTool` | keep (as flat allowlist) | E1 | Interim safety until E3 policy hook |
| `Grants.AllowsEnv` | drop | E1 | Lattice env authority check |
| `RootGrants` | drop | E1 | |
| `SubsetOf` | drop | E1 | |
| `CanonicalTools` | keep | — | Existence-oracle defense is loop correctness |
| `ParticipantKind` (+ `ParticipantHuman` / `ParticipantAgent`) | drop | E1 | Human roster UX |
| `PresenceState` (+ `PresenceActive` / `PresenceIdle` / `PresenceLeft`) | drop | E1 | |
| `Participant` / `ParticipantStore` | drop | E1 | |
| `SessionMemoryEntry` / `SessionMemoryStore` / `ValidMemoryKey` | keep | E1 moves toward `memory.go` | Loop scratchpad |
| `MaxMemoryKeys` / `MaxMemoryValue` / `MaxMemoryKeyBytes` | keep | — | Enforced memory caps |
| `RunWait` / `RunWaitMember` / `WaitMemberResult` / `WaitOutcome` / `RunWaitStore` | keep | — | Spawn/wait/cohort orchestration |
| Wait state constants (`WaitOpen` / `WaitResolved` / `WaitTimedOut` / `WaitAbandoned`) | keep | — | |
| Wait kind constants (`WaitKindWait` / `WaitKindCohort`) | keep | — | |
| Timeout policy constants (`TimeoutPolicyResolve` / `TimeoutPolicyFail`) | keep | — | |

### 1.7 `automation.go` — periodic prompts

| Symbol | Disposition | Phase | Notes |
|---|---|---|---|
| `PeriodicPromptID` / `PeriodicPrompt` / `PeriodicPromptStore` | keep | — | Primer needs them; small + generic |

### 1.8 `credential.go` — BYO inference credentials

| Symbol | Disposition | Phase | Notes |
|---|---|---|---|
| `CredentialKindOpenAI` / `CredentialKindAnthropic` / `CredentialKindBedrock` | keep | — | Inference kind constants |
| `InferenceCredentialKind` | keep | — | provider → kind map |
| `Credential` / `CredentialInfo` / `InferencePayload` / `CredentialStore` | keep | — | Tenant BYO inference |

### 1.9 `capability.go` — provider capability probing

| Symbol | Disposition | Phase | Notes |
|---|---|---|---|
| `ProviderCapability` type | keep | — | Capability token type |
| `CapabilityRestartPreservesWorkspace` | keep | — | Optional behavioral cap |
| `CapabilityToleratesDisconnect` | keep | — | |
| `CapabilityAdoptsOrphans` | keep | — | |
| `CapabilityEnumeratesResources` | keep | — | |
| `CapabilityServesToolEndpoint` | keep | — | |
| `CapabilityNamespaceIsolation` | drop | E1 | Hosted-EKS isolation product |
| `CapabilityResourceQuota` | drop | E1 | Hosted quota product |
| `OptionalProviderCapabilities` / `CoreProviderContract` | keep | E1 drops hosted-only entries from optional list | |
| `ProviderCapabilities` / `Has` / `Reason` | keep | — | |
| `CapabilityProber` | keep | — | |

### 1.10 `auth.go` — authentication

| Symbol | Disposition | Phase | Notes |
|---|---|---|---|
| `Authenticator` | keep (reshape) | E3 | Becomes API-key authenticator |
| `DevTokenAuthenticator` / `ParseDevTokens` | drop (dev-only human tokens) | E3 | Replaced by tenant API keys; test harness adapts |

### 1.11 `store.go` — store seams

| Symbol | Disposition | Phase | Notes |
|---|---|---|---|
| `ErrNotFound` / `ErrAlreadyExists` / `ErrPermissionDenied` | keep | — | |
| `Store` | keep | E3 renames `Org` → `Tenant` | |
| `OrgStore` | keep (reshape) | E3 | Becomes tenant store; membership methods drop |
| `UserStore` | drop | E3 | |
| `OrgScope` | keep (rename → `TenantScope`) | E3 | |
| `OrgScope.Usage()` | drop | E1 | |
| `OrgScope.Participants()` | drop | E1 | |
| `OrgScope.Flows()` | drop | E1 | |
| `OrgScope.Envs()` | generalize → `Resources()` | E2 | |
| `OrgScope.Sessions/Events/Runs/Credentials/Providers/Memory/Waits/PeriodicPrompts` | keep | — | |
| `SessionStore` / `EventStore` | keep | — | |

### 1.12 `secrets/`

| Symbol | Disposition | Phase | Notes |
|---|---|---|---|
| `Keyring` / `AESKeyring` / `GenerateMasterKey` | keep | — | |
| `Redactor` / `RedactingHandler` / `Encodings` | keep | — | Credential non-leak |

---

## 2. Packages

### 2.1 `loop/`

| Path | Disposition | Phase | Notes |
|---|---|---|---|
| `loop/loop.go` | keep | — | Owned agent loop |
| `loop/step.go` | keep | E1 flat allowlist check | One job per step |
| `loop/model.go` | keep | — | Fantasy provider wiring + credentials |
| `loop/spawn.go` | keep | E1 removes flow-catalog spawn | `spawn_agent` / cohort stay |
| `loop/waits.go` | keep | — | |
| `loop/waittimeout.go` | keep | — | Durable wait sweeper |
| `loop/memorytools.go` | keep | — | |
| `loop/envtools.go` | generalize | E2 | Resource tools; name follows resource |
| `loop/automation.go` | keep | — | Cost-accounting hook |
| `loop/presence.go` | drop | E1 | Presence reaper |

### 2.2 `envwork/` / `flowwork/`

| Path | Disposition | Phase | Notes |
|---|---|---|---|
| `envwork/` (whole package) | generalize → `resourcework/` | E2 | Lifecycle already generic in behavior |
| `flowwork/` (whole package) | drop / move-to-consumer | E1 | Flow advance state machine → curri |

### 2.3 `envprovider/*`

| Path | Disposition | Phase | Notes |
|---|---|---|---|
| `envprovider/registry.go` | keep | E2 may rename package | Tenant-scoped adapter registry |
| `envprovider/registry_test.go` | keep | E1 drops hosted kind assertions | |
| `envprovider/wiring.go` | keep | E1 removes hosted_eks registration | |
| `envprovider/conformance/` | keep | E1 drops hosted-only capability rows | Shared conformance suite |
| `envprovider/localdocker/` | keep | — | |
| `envprovider/k8s/k8s.go` | keep | — | `byo_k8s` entry |
| `envprovider/k8s/lifecycle.go` (BYO path) | keep | — | Shared lifecycle; hosted branches stripped in E1 |
| `envprovider/k8s/k8s_test.go` / `reconcile_test.go` | keep | E1 drops hosted-only cases | |
| `envprovider/k8s/hosted.go` | drop | E1 | Hosted-EKS isolation |
| `envprovider/k8s/hosted_test.go` | drop | E1 | |
| Hosted-only bits inside `k8s/lifecycle.go` (namespace isolation policy, quota enforce, ingress CIDR) | drop | E1 | BYO path remains |
| `envprovider/localdocker/local.go` + `_test.go` | keep | — | |
| `envprovider/nomad/nomad.go` + tests | keep | — | `byo_nomad` |
| `envprovider/static/static.go` + `_test.go` | keep | — | |
| `envprovider/tunnel/` (`agent.go`, `broker.go`, `protocol.go`, `provider.go` + tests) | keep | — | |

### 2.4 `jobqueue/*`

| Path | Disposition | Phase | Notes |
|---|---|---|---|
| `jobqueue/jobqueue.go` | keep | — | Seam |
| `jobqueue/conformance/` | keep | — | |
| `jobqueue/inproc/` | keep | — | Dev/test queue |
| `jobqueue/river/` | keep | — | Production queue |

### 2.5 `postgres/`

| Path | Disposition | Phase | Notes |
|---|---|---|---|
| `postgres/store.go` | keep | E3 rename Org→Tenant | Root store + tx |
| `postgres/session.go` | keep | — | Sessions + scope wiring |
| `postgres/eventbus.go` | keep | — | NOTIFY wakeup + gapless read |
| `postgres/run.go` | keep | E1 strip flow columns usage | |
| `postgres/org.go` | keep (reshape) | E1 drop Plan; E3 drop members/users | |
| `postgres/env.go` | generalize | E1 drop usage store; E2 resource rename | Env + provider + usage today |
| `postgres/multiplayer.go` | split | E1 drop participants; keep memory + waits | |
| `postgres/flow.go` | drop | E1 | |
| `postgres/periodic.go` | keep | — | |
| `postgres/enqueue.go` | keep | — | Txal enqueue helper |
| `postgres/store_test.go` | keep | E1/E2/E3 trim deleted surfaces | |
| `postgres/migrations/*` | squash | E4 | See migrations axis; interim code drops precede squash |

### 2.6 `http/`

| Path | Disposition | Phase | Notes |
|---|---|---|---|
| `http/server.go` | keep | E1 unregisters dropped services | |
| `http/auth.go` | keep (reshape) | E3 | |
| `http/convert.go` / `convert_test.go` | keep | E1 drops flow/plan/billing mappings | |
| `http/session_handler.go` | keep | E1 gains memory RPCs from multiplayer | |
| `http/agent_handler.go` | keep | — | |
| `http/env_handler.go` (env RPCs) | generalize | E2 | |
| `http/env_handler.go` (`billingHandler` / `GetUsage`) | drop | E1 | |
| `http/org_handler.go` | keep (reshape) | E1 drop Plan; E3 API keys | Credentials + providers stay |
| `http/automation_handler.go` | keep | — | |
| `http/multiplayer_handler.go` | drop (fold survivors) | E1 | Presence RPCs die; memory → session |
| `http/flow_handler.go` | drop | E1 | |

### 2.7 `mcp/` / `secrets/`

| Path | Disposition | Phase | Notes |
|---|---|---|---|
| `mcp/client.go` | keep | — | Tool surface inside a resource |
| `mcp/cache.go` / `cache_test.go` | keep | — | |
| `secrets/` | keep | — | See root types §1.12 |

### 2.8 `testkit/*`

| Path | Disposition | Phase | Notes |
|---|---|---|---|
| `testkit/harness/` | keep | E1 drops web/desktop stack legs as needed | Real-stack harness |
| `testkit/pgtest/` | keep | — | |
| `testkit/modelscript/` | keep | — | Only permitted fake |
| `testkit/testclient/` | keep | E1 drops flow/billing helpers | Primary client evidence post-E1 |
| `testkit/envconverge/` | generalize | E2 | Name follows resource |

### 2.9 `clients/*`

| Path | Disposition | Phase | Notes |
|---|---|---|---|
| `clients/ts/` | keep | E4 grows into TS SDK | Seed client; keep `e2e/ts_smoke_test.go` |
| `clients/rust/` | drop | E1 | GPUI app dependency / shared rust client for desktop |

### 2.10 `cmd/*`

| Path | Disposition | Phase | Notes |
|---|---|---|---|
| `cmd/cored/` | keep (rename → `cored` at end of E1) | E1 rename | API daemon |
| `cmd/coreworker/` | keep (rename → `coreworker`) | E1 rename | |
| `cmd/core/` (`main.go`) | keep (rename → `core`) | E1 | CLI entrypoint |
| `cmd/core/cli/cli.go` | keep | E1 strips flow verbs | |
| `cmd/core/cli/provider.go` | keep | — | Provider onboarding |
| `cmd/core/cli/cli_test.go` / `onboarding_test.go` | keep | E1 trims flow cases | |
| `cmd/core/cli` flow subcommands | drop | E1 | |
| `cmd/devstack/` | keep | E1 drops web leg | pg + model + daemon + worker |
| `cmd/core-env-agent/` | keep (generalize name later) | E2 optional rename | Tunnel/static agent helper |

### 2.11 `ui/*`

| Path | Disposition | Phase | Notes |
|---|---|---|---|
| `ui/web/` (React SPA + Playwright) | drop | E1 | Consumers bring UI |
| `ui/desktop/` (GPUI) | drop | E1 | Consumers bring UI |

### 2.12 Ancillary trees (not in the phase table but walked)

| Path | Disposition | Phase | Notes |
|---|---|---|---|
| `examples/flows/` | drop / move-to-consumer | E1 | Ships with flow deletion |
| `docs/flows.md` | drop | E1 | |
| `docs/security.md` lattice sections | drop / rewrite | E1 | |
| `docs/providers.md` | keep (trim hosted) | E1 | |
| `docs/onboarding-kubernetes.md` | keep (trim hosted) | E1 | |
| `agent_docs/flows.md` / `multiplayer.md` | drop | E1 | |
| `agent_docs/core_extraction_plan/` | keep | — | This plan |
| `plan/` (legacy product roadmap) | keep as history | — | Not live code; fences exclude it |
| `gen/` | regenerate | E1/E4 | Generated; not hand-edited |
| `scripts/verify-*.sh|py` | keep | E1 updates coverage schema; rust verify script dies with `clients/rust` | |
| `scripts/check-extraction-fences.sh` | keep | E0 | New in E0 |
| `scripts/dev-stack.sh` / `mutate-*.sh` | keep | E1 trims product-only legs | |
| `.github/workflows/ci.yml` / `scheduled.yml` | keep | E1 drops web/desktop jobs; provider legs stay | |
| `go.mod` / `go.sum` | keep (rename module end of E1) | E1 | Per decision D8 → `github.com/aleksclark/ultracore` |
| `buf.yaml` / `buf.gen.yaml` | keep | E4 package path → `core/v1` | |
| `Taskfile.yml` | keep | E0 fence wiring; E1+ task renames | |
| `.tool-versions` | keep | E0 | Local asdf pins (golang/task/nodejs) |
| `README.md` / `PLAN.md` / `AGENTS.md` | keep (rewrite) | E1 | Product framing → core runtime framing |
| `agent_docs/*.md` (non-plan) | keep / drop per topic | E1 drops `flows.md` / `multiplayer.md`; rest rewrite | |

---

## 3. Protos (`proto/ultra/v1/`)

| File | Disposition | Phase | Notes |
|---|---|---|---|
| `event.proto` | keep (trim + rename) | E1 drops flow/presence events; E2 resource events; E4 `core.v1` reshape | Event log contract |
| `session.proto` | keep | E1 drops Join/Leave/Heartbeat/ListParticipants; keeps memory RPCs; E3 labels | |
| `agent.proto` | keep | — | Run + run-tree RPCs |
| `env.proto` `EnvService` | generalize | E2 | Becomes resource service |
| `env.proto` `BillingService` / `GetUsage` | drop | E1 | |
| `org.proto` | keep (reshape) | E1 drop Plan field; E3 API keys replace members | Credentials + providers stay |
| `automation.proto` | keep | — | Periodic prompts |
| `flow.proto` | drop / move-to-consumer | E1 | Entire FlowService |

Mechanical package rename `ultra.v1` → `core.v1` is E1 end (stub); real
surface reshape is E4.

---

## 4. Migrations (`postgres/migrations/`)

There are no production deployments (decision D7). Code drops happen in E1–E3;
schema is squashed to a fresh baseline in E4. Rows below tag what each
migration *contributed* and when its concepts leave the live schema.

| Migration | Contents (summary) | Disposition | Phase |
|---|---|---|---|
| `00001_init.sql` | `orgs` (incl. `plan`), `users`, `org_members`, `sessions`, `session_events` | keep sessions/events; drop `plan` E1; drop users/members E3; rename org→tenant E3/E4 squash | E1 / E3 / E4 |
| `00002_runs_credentials.sql` | `credentials`, `agent_runs`, `agent_run_steps` | keep | E4 squash retains |
| `00003_envs.sql` | `provider_instances`, `dev_envs`, `env_usage` | keep providers + envs (→resources E2); **drop `env_usage`** E1 | E1 / E2 / E4 |
| `00004_multiplayer.sql` | run parent/grants/result, `participants`, `session_memory`, `run_waits` (+ members) | keep memory + waits + parent/result; drop participants E1; simplify grants E1 | E1 / E4 |
| `00005_flows.sql` | `flows`, `flow_invocations`, flow FK columns on runs/envs | drop | E1 / E4 squash omits |
| `00006_automation.sql` | `hook_cursors`, `periodic_prompts` | keep | E4 squash retains |
| `00007_orchestration.sql` | spawn_key, cohort_*, wait kind/timeout/resumed | keep | E4 squash retains |
| `00008_flow_completion.sql` | flow definition text, invocation state machine, progress, flow_agent/env names | drop | E1 / E4 squash omits |
| `00009_provider_capabilities.sql` | `provider_instances.capabilities` | keep | E4 squash retains |

---

## 5. Loop tools (`ultra.CanonicalTools()`)

Source: `multiplayer.go` `CanonicalTools()`.

| Tool | Disposition | Phase | Notes |
|---|---|---|---|
| `ask_user` | keep | — | Event + awaiting state; UI is consumer-side |
| `post_event` | keep | — | |
| `session_memory_get` | keep | — | |
| `session_memory_list` | keep | — | |
| `session_memory_set` | keep | — | |
| `session_memory_delete` | keep | — | |
| `spawn_agent` | keep | E1 loses flow-catalog form | Explicit per-call agent specs remain |
| `wait_for_agents` | keep | — | |
| `run_agent_cohort` | keep | — | |
| `provision_env` | generalize | E2 | Becomes resource provision tool |
| `list_envs` | generalize | E2 | |
| `terminate_env` | generalize | E2 | |
| `bash` | keep | — | Bezalel MCP tool |
| `view` | keep | — | |
| `write` | keep | — | |
| `edit` | keep | — | |
| `multiedit` | keep | — | |
| `delete` | keep | — | |
| `ls` | keep | — | |
| `glob` | keep | — | |
| `grep` | keep | — | |
| `job_output` | keep | — | |
| `job_kill` | keep | — | |
| `download` | keep | — | |
| `fetch` | keep | — | |
| `web_fetch` | keep | — | |
| `lsp_diagnostics` | keep | — | |
| `lsp_references` | keep | — | |
| `lsp_restart` | keep | — | |

---

## 6. Event types

See §1.4 for the full vocabulary with dispositions. Summary counts at
`16629b5`:

| Bucket | Count | Phase action |
|---|---|---|
| Keep as-is (run/stream/memory/automation/denial) | **21** kinds | — |
| Generalize env_* + `exec_preview_ran` → resource_* | **8** kinds | E2 |
| Drop presence (participant_*) | **3** kinds | E1 |
| Drop / move-to-consumer flow_* | **3** kinds | E1 |
| **Total `EventKind*` constants** | **35** | matches `event.go` |

---

## 7. e2e tests

Cross-referenced with `e2e/coverage.json` capabilities (web/rust columns are
first-party UI evidence removed in E1). Go functional files are the lasting
evidence base; post-E1 coverage schema keeps Go (+ later SDK) only.

### 7.1 Go functional files (`e2e/*.go`)

| File | Disposition | Phase | Notes / coverage link |
|---|---|---|---|
| `functional_test.go` | keep | — | Session roundtrip, fanout, resume, tenancy, org lifecycle |
| `agent_test.go` | keep | E1 gains ported denial assertions | Happy path, durability, awaiting, cancel, streaming, BYO creds |
| `env_test.go` | keep (trim metering) | E1 drops metering asserts; E2 rename | Lifecycle + exec; metering lines die with billing |
| `automation_test.go` | keep | — | Periodic prompt API |
| `credential_rotation_test.go` | keep | — | Rotation + non-leak |
| `provider_test.go` | keep | E1 drops hosted-only cases if any | Registration + probe refusal |
| `provider_capability_test.go` | keep | E1 trims hosted caps | Behavioral capabilities |
| `advanced_loop_test.go` | keep | — | Compaction + hook |
| `phase7_test.go` | keep (trim) | E1 drops metering-only / UI-gate-only pieces that die with clients | Durability, rotation, reconcile, codegen gates |
| `phase8_memory_test.go` | keep | E1 absorbs presence-free memory asserts from multiplayer | Memory + (today) presence |
| `phase8_orchestration_test.go` | keep | E1 adjusts grant-narrowing → flat allowlist | Spawn, cohort |
| `phase8_waits_test.go` | keep | — | Wait race matrix |
| `phase8_replicas_test.go` | keep | — | Cross-replica subscribe + worker takeover |
| `phase8_throughput_test.go` | keep | — | Throughput baseline |
| `phase8_security_test.go` | keep (trim lattice docs) | E1 | Security doc assertions |
| `phase8_grants_test.go` | drop (port denial asserts) | E1 | Lattice enforcement → flat allowlist tests in agent_test |
| `grants_test.go` | drop (port) | E1 | |
| `multiplayer_test.go` | drop (port memory/run-tree) | E1 | Presence dies; memory/run isolation ported |
| `flow_test.go` | drop / move-to-consumer | E1 | |
| `flow_lifecycle_test.go` | drop / move-to-consumer | E1 | |
| `flow_versioning_test.go` | drop / move-to-consumer | E1 | |
| `flow_convergence_test.go` | drop / move-to-consumer | E1 | |
| `web_test.go` | drop | E1 | Playwright runner |
| `rust_desktop_test.go` | drop | E1 | GPUI runner |
| `ts_smoke_test.go` | keep | E4 expands | TS client smoke |
| `coverage.json` | keep (rewrite schema) | E1 drops web/rust columns; E4 adds SDK columns | |

### 7.2 `coverage.json` capabilities

| Capability | Disposition | Phase | Notes |
|---|---|---|---|
| `auth_org_sessions` | keep (reshape) | E3 auth | Go functional remains evidence |
| `connection_state` | keep | E1 drops web/rust cols | Subscribe path |
| `incremental_streaming` | keep | E1 | |
| `event_replay` | keep | E1 | |
| `agent_stream_and_await` | keep | E1 | |
| `credential_gateway_fields` | keep | E1 | |
| `credential_redaction` | keep | E1 | |
| `dev_env_exec_usage` | generalize (trim usage) | E1 drops usage UI; E2 rename | Exec stays |
| `dev_env_restart_rotation` | generalize | E2 | |
| `env_usage_metering` | drop | E1 | Billing |
| `flow_authoring_validation` | drop | E1 | |
| `flow_cancellation_recovery` | drop | E1 | |
| `flow_catalog_versions` | drop | E1 | |
| `flow_direct_invocation` | drop | E1 | |
| `flow_invocation_progress` | drop | E1 | |
| `presence` | drop | E1 | |
| `prompt_input` | keep (consumer UI) / drop client cols | E1 | Core keeps PromptRun RPC evidence via Go |
| `provider_failure_validation` | keep | E1 | |
| `provider_ownership_and_hosting` | keep (trim hosted wording) | E1 | Ownership stays; hosted product goes |
| `provider_registration_kinds` | keep | E1 drops hosted_eks kind | |
| `replica_reconnect` | keep | E1 | |
| `run_lane_filter` | keep (Go evidence) | E1 drops UI cols | |
| `run_tree_linkage` | keep | E1 | |
| `session_memory` | keep | E1 | |
| `shadcn_dark_surface` | drop | E1 | First-party web chrome |
| `agent_memory_inspection` | keep | E1 | |
| `wait_transitions` | keep | E1 | |

### 7.3 UI e2e specs (deleted with clients)

| Path | Disposition | Phase |
|---|---|---|
| `ui/web/e2e/advanced-loop.spec.ts` | drop | E1 |
| `ui/web/e2e/automation.spec.ts` | drop | E1 |
| `ui/web/e2e/environment.spec.ts` | drop | E1 |
| `ui/web/e2e/flows.spec.ts` | drop | E1 |
| `ui/web/e2e/multiplayer.spec.ts` | drop | E1 |
| `ui/web/e2e/providers.spec.ts` | drop | E1 |
| `ui/web/e2e/run-tree.spec.ts` | drop | E1 |
| `ui/web/e2e/session.spec.ts` | drop | E1 |
| `ui/web/e2e/settings.spec.ts` | drop | E1 |
| `ui/desktop/tests/desktop_e2e.rs` | drop | E1 |
| `ui/desktop/tests/environment_e2e.rs` | drop | E1 |
| `ui/desktop/tests/flow_e2e.rs` | drop | E1 |
| `ui/desktop/tests/provider_e2e.rs` | drop | E1 |
| `ui/desktop/tests/run_tree_e2e.rs` | drop | E1 |
| `ui/desktop/tests/support/mod.rs` | drop | E1 |

### 7.4 Deferred RPCs in `coverage.json`

| Deferred RPC (from `e2e/coverage.json`) | Disposition | Phase | Notes |
|---|---|---|---|
| `ultra.v1.SessionService/Leave` | drop with presence | E1 | |
| `ultra.v1.SessionService/GetMemory` | keep (needs Go evidence entry post-E1) | E1 | Already proven in Go suite |
| `ultra.v1.SessionService/DeleteMemory` | keep | E1 | |
| `ultra.v1.OrgService/CreateOrg` | keep as tenant admin via API keys | E3 | |
| `ultra.v1.OrgService/InviteMember` | drop | E3 | Human membership |
| `ultra.v1.OrgService/ListMembers` | drop | E3 | |
| `ultra.v1.AutomationService/PutPeriodicPrompt` | keep | — | Evidence completes when scheduler is productized; API stays |
| `ultra.v1.AutomationService/ListPeriodicPrompts` | keep | — | |
| `ultra.v1.AutomationService/SetPeriodicPromptEnabled` | keep | — | |

---

## 8. Ambiguity resolutions (locked in E0)

These were called out as ambiguous in the phase plan and are resolved here so
E1 does not renegotiate mid-deletion.

| Item | Resolution | Rationale |
|---|---|---|
| `ask_user` | **keep** as event + `run_awaiting` state | Substrate concern; only the UI widget is consumer-side |
| `automation.go` / periodic prompts | **keep** | Primer floor proof (E5) needs them; 26-line generic store |
| Session memory | **keep** (not “multiplayer”) | Loop scratchpad; move out of multiplayer file in E1 |
| Run trees / waits / cohort | **keep** | Owned-loop orchestration, not human multiplayer |
| `Grants` | **drop lattice in E1**, keep flat tool allowlist | E3 policy hook is the real replacement; interim safety required |
| `CanonicalTools` + denial stubs | **keep** | Existence-oracle defense is loop correctness |
| `clients/ts` | **keep** | Seed of TS SDK (E4); rust client drops with GPUI |
| `cmd/core-env-agent` | **keep** | Required by tunnel/static providers |
| `AgentRun.Grants` field name | flat `Tools` (or keep struct with only Tools) in E1 | Implementer choice inside E1; lattice fields must die |
| Human `User` / org membership | **drop in E3** (not E1) | E1 can still boot with dev tokens; E3 replaces identity |
| `Org` naming | stay through E1–E2; **rename Tenant in E3** | Avoid rename noise during mass deletion |
| Hosted bits inside shared `k8s/lifecycle.go` | **drop hosted branches in E1** | File may remain for BYO |
| `RateClass` on provider instances | **drop with metering in E1** | No billing substrate |
| `examples/flows` + flow CLI | **drop with flows in E1** | move-to-consumer documentation lives in curri |
| Module rename (`ultracore`) | **end of E1** after deletions | Per decision D8 |
| Migrations | code stop-using in E1–E3; **squash in E4** | No prod deployments |

---

## 9. Spot-check (acceptance A0.2)

| Target | Expected | Inventory row |
|---|---|---|
| `flowdef.go` | drop / E1 | §1.5 |
| `envwork/` | generalize / E2 | §2.2 |
| `jobqueue/river` | keep | §2.4 |
| `envprovider/k8s/hosted.go` | drop / E1 | §2.3 |
| `automation.go` | keep | §1.7 |

---

## 10. Inventory audit method

Rows were produced by:

1. Listing every root `*.go` and reading exported `type`/`const`/`func` decls.
2. Walking `loop/`, `envwork/`, `flowwork/`, `envprovider/**`, `jobqueue/**`,
   `postgres/**`, `http/`, `mcp/`, `secrets/`, `testkit/**`, `clients/**`,
   `cmd/**`, `ui/**`.
3. Listing `proto/ultra/v1/*.proto` services/rpcs and
   `postgres/migrations/00001–00009`.
4. Expanding `CanonicalTools()` and every `EventKind*` constant.
5. Listing all `e2e/*.go` test files and every `coverage.json` capability key
   plus deferred RPCs.
6. Listing `go list ./...` package tops, UI e2e specs by filename, deferred
   RPCs by full `ultra.v1.*` key, and ancillary roots (`scripts/`,
   `.github/`, `go.mod`, buf config, top-level docs).

A later phase may only change a row by editing this file in the same PR that
executes the disposition.

### Live counts at `16629b5` (review cross-check)

| Axis | Count |
|---|---|
| Root `*.go` (incl. tests) | 14 |
| Root + `secrets/` exported types | 110 (106 root non-test + 4 secrets) |
| `go list ./...` packages | 33 (incl. `gen/`) |
| `proto/ultra/v1/*.proto` | 7 |
| `postgres/migrations/*.sql` | 9 |
| `CanonicalTools()` | 29 |
| `EventKind*` | 35 |
| `e2e/*.go` | 25 |
| `coverage.json` capabilities | 27 |
| `coverage.json` deferred RPCs | 9 |
| `ui/web/e2e/*.spec.ts` | 9 |
| `ui/desktop/tests/*_e2e.rs` | 5 (+ support) |
