# Phase E1 inventory — behavior → test map

Behavior coverage that must remain true after shedding the product surface.
Every row is either proven by a surviving test or explicitly weakened with an
E3 follow-up.

| Behavior | Test | Tier |
|---|---|---|
| Session roundtrip (create/get/list) | `e2e/functional_test.go` `TestA01_SessionRoundtrip` | functional |
| Event fanout via Subscribe | `e2e/functional_test.go` `TestA02_AppendFanout` | functional |
| Resume/replay from seq | `e2e/functional_test.go` `TestA03_ResumeContract` | functional |
| Cross-tenant denial indistinguishable | `e2e/functional_test.go` `TestA06_TenantIsolation` | functional |
| Agent happy path + streaming | `e2e/agent_test.go` `TestA11` / `TestA15` | functional |
| Durability under worker kill | `e2e/agent_test.go` `TestA12_DurabilityUnderSIGKILL` | functional |
| ask_user awaiting without parked workers | `e2e/agent_test.go` `TestA13_AwaitingWithoutParkedWorkers` | functional |
| Cancellation | `e2e/agent_test.go` `TestA14_Cancellation` | functional |
| BYO credentials + redaction | `e2e/agent_test.go` `TestA17` / `TestCredentialRPCs` | functional |
| Flat allowlist denial (uniform stub, PermissionDenied event, no existence leak) | `e2e/agent_test.go` `TestE1_FlatAllowlistDenialVisibility` | functional |
| Child inherits parent allowlist verbatim | `e2e/phase8_orchestration_test.go` `TestA81_SpawnDurabilityAndGrants` + loop spawn unit path | functional |
| Env provision + tool use through Bezalel | `e2e/env_test.go` `TestA22_A23_AgentEnvPersistence` | functional |
| Env restart/token rotation | `e2e/phase7_test.go` `TestA74_EnvDurabilityAndRotation` | functional |
| Session memory CRUD + caps + concurrency | `e2e/phase8_memory_test.go` `TestA86_SessionMemory` | functional |
| Run trees + spawn idempotency | `e2e/phase8_orchestration_test.go` `TestA81_*` | functional |
| Cohort fan-out/fan-in | `e2e/phase8_orchestration_test.go` `TestA83_*` | functional |
| Wait race matrix | `e2e/phase8_waits_test.go` `TestA82_WaitRaceMatrix` | functional |
| Cross-replica subscribe + worker takeover | `e2e/phase8_replicas_test.go` | functional |
| Periodic prompts API | `e2e/automation_test.go` `TestA66_PeriodicPromptAPI` | functional |
| Provider registration + probe refusal | `e2e/provider_test.go` | functional |
| Provider conformance ×5 kinds (localdocker, byo_k8s, byo_nomad, static, tunnel) | `envprovider/*/…` + CI provider legs | conformance |
| CLI uses only public API | `cmd/core/cli/cli_test.go` `TestCLIUsesOnlyPublicAPIs` | CLI |
| TS client smoke | `e2e/ts_smoke_test.go` | functional |
| Orphaned-surface detection | `scripts/check-extraction-fences.sh` via `task lint` | CI |
| Coverage matrix integrity | `scripts/verify-coverage.py` + mutation gate | CI |

## Intentionally weakened (E3 follow-up)

| Behavior | E1 state | E3 plan |
|---|---|---|
| Grants lattice (SubsetOf, EnvAll, MaySpawn, MaxChildren, RootGrants, cohort grant inheritance) | Collapsed to flat per-run `Tools []string` allowlist; `"*"` = all; children inherit parent allowlist verbatim | Consumer policy hook on top of the allowlist |
| Human presence / participant roster | Deleted | Consumers own human identity and UI |
| Billing / metering / Org.Plan | Deleted | Future product layer if ever |
| Hosted EKS isolation | Deleted; `byo_k8s` remains | — |
| Flows / flowdef catalog | Deleted; spawn_agent keeps explicit per-call agent specs | curri-agents / primer own trigger+template |
| First-party web SPA + GPUI desktop | Deleted; Go functional + TS smoke are client evidence | E4 grows Go/TS SDKs |

## Dropped e2e (with ports)

| Deleted file | Ported into |
|---|---|
| `e2e/grants_test.go` | lattice unit checks removed; denial → `TestE1_FlatAllowlistDenialVisibility` |
| `e2e/phase8_grants_test.go` | denial visibility → `agent_test.go`; helpers `toolResult`/`toolResultsFor` ported |
| `e2e/multiplayer_test.go` | memory/run-tree already covered by `phase8_memory_test.go` / orchestration |
| `e2e/flow_*.go` (4) | dropped with flows |
| `e2e/web_test.go`, `e2e/rust_desktop_test.go` | dropped with clients |
| `e2e/provider_capability_test.go` | flow-based capability invoke tests dropped |
| `TestA76_MeteringAndTenancy` | dropped with billing |
