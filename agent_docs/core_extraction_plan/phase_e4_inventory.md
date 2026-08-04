# Phase E4 inventory — Consumer surface: API v1, SDKs, ops hardening

**Phase:** E4  
**Branch:** `core-extraction`  
**Date:** 2026-08-03  

## Acceptance → implementation map

| ID | Acceptance | Implementation | Test evidence |
|---|---|---|---|
| A4.1 | Full suite green incl. sdk:test, verify:coverage v2, buf breaking, fences | Taskfile `sdk:test`; `scripts/verify-coverage.py` v2; fences `e4.txt`; CI sdk-test job | unit suite; `TestA45/A46/A48`; `TestA79_EvidenceIntegrity`; fences script |
| A4.2 | Every capability has go_functional + go_sdk + ts_sdk | `e2e/coverage.json` v2 matrix (33 caps × 3 legs) | `verify:coverage`; mutation gate |
| A4.3 | Fresh-clone `task dev` boots from baseline | `00001_baseline.sql`; `scripts/dev-stack.sh` | `task dev:smoke` (CI job) |
| A4.4 | docker-compose + /readyz when pg stopped | `docker-compose.yml`, `/healthz`+`/readyz` on cored; worker health via `CORE_ADDR` | deploy docs; compose file |
| A4.5 | Subscribe-resume no gaps/duplicates Go+TS | `sdk.Subscribe` reconnect; `testclient.SubscribeResume`; TS `subscribe` | `TestA45_SDKSubscribeResume`; TS smoke resume it |
| A4.6 | Replay equality Go vs TS | `TestA46_ReplayParityGoVsTS` + TS getEvents dump | e2e parity test |
| A4.7 | Unknown `CORE_*` refuses startup | `config.RefuseUnknown` | `config/config_test.go` |
| A4.8 | v0.1.0 readiness + buf breaking | Docs + CI PR breaking gate; **tag left for human** | audit note |

## Work items

### T4.1 Proto reshape
- Split `org.proto` → `tenant.proto`, `credential.proto`, `provider.proto`
- Rename `agent.proto` → `run.proto` (`AgentService` → `RunService`, `PromptRun` → `AnswerRun`)
- `EventService.Get` range RPC; `SessionService.ArchiveSession`
- Pagination fields on list RPCs
- `task generate` → `gen/go` + `clients/ts/src/gen`

### T4.2 Schema squash
- Single `postgres/migrations/00001_baseline.sql`
- Final names: `tenants`, `tenant_id`, `resources` (was `dev_envs`), no users/flows/env_usage
- Store SQL updated in lockstep

### T4.3 Go SDK
- `sdk/`: client, events (reconnect), runs (await), labels
- `testkit/testclient` embeds `*sdk.Client`; functional suite exercises SDK

### T4.4 TS SDK
- `clients/ts` `@ultracore/client` with `createClient` ergonomics
- Expanded `smoke.test.ts` + `sdk_surface.test.ts`

### T4.5 Deploy + config
- `config/` package, refuse unknown `CORE_*`
- `/healthz` + `/readyz` on cored; worker health opt-in via `CORE_ADDR`
- `Dockerfile`, `docker-compose.yml`, systemd + Nomad examples
- `docs/deploy.md`

### T4.6 Coverage v2 + docs
- coverage.json legs; verify-coverage.py; mutate gate
- README, consumers.md, deploy.md, AGENTS.md

## New tests

| Test | File | Behavior |
|---|---|---|
| `TestA45_SDKSubscribeResume` | e2e/sdk_resume_test.go | resume no gaps/duplicates + GetEvents |
| `TestA46_ReplayParityGoVsTS` | e2e/replay_parity_test.go | Go vs TS event kind/seq parity |
| `TestA48_SessionArchive` | e2e/sdk_resume_test.go | ArchiveSession |
| `TestRefuseUnknown` | config/config_test.go | unknown CORE_* |
| TS smoke + surface | clients/ts/*.test.ts | all ts_sdk legs |
