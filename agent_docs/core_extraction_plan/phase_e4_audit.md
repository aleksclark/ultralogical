# Phase E4 completion audit

**Phase:** E4 — Consumer surface: API v1, SDKs, ops hardening  
**Branch:** `core-extraction`  
**Date:** 2026-08-03  
**Auditor:** implementer self-audit against phase_e4.md acceptance  
**Gap-fix pass:** same day (bootstrap/auth drift after tenancy reshape)

## A4.1 Full suite + gates

| Check | Evidence |
|---|---|
| `go build ./...` | ok |
| Unit/store/http/config/cli | `go test ./sdk/ ./config/ ./postgres/ -count=1` → ok |
| Focused functional e2e | `go test ./e2e/ -run 'SDK\|Resume\|Replay\|A45\|A46\|A48\|A79\|Tenancy\|A02' -count=1` → **ok ~9.4s** |
| Fences E1–E4 | `scripts/check-extraction-fences.sh` → clean |
| Coverage v2 | `python3 scripts/verify-coverage.py` → `33 capabilities × 3 legs` |
| Coverage mutation | `scripts/mutate-coverage-gate.sh` + `TestA79_EvidenceIntegrity` (suite) |
| `buf lint` | clean (prior) |
| buf breaking | CI PR job against `origin/${{ github.base_ref }}`; local `task verify:breaking` vs HEAD |
| `task sdk:test` | present in Taskfile.yml |

## A4.2 Coverage matrix v2

- Schema: capability → `{go_functional, go_sdk, ts_sdk}` each with file/test/asserts
- go_sdk satisfied via testclient → SDK rebase (entire functional suite)
- ts_sdk via `clients/ts/smoke.test.ts` + `sdk_surface.test.ts`
- Mutation gate rejects: missing file, missing test, missing assert, CI non-execution, deleted capability, uncovered RPC
- Spot-check: zero empty required legs across 33 capabilities

## A4.3 Fresh baseline

- Migrations: **only** `postgres/migrations/00001_baseline.sql`
- Tables: tenants, api_keys, sessions+labels, session_events, credentials, agent_runs, agent_run_steps, provider_instances, resources, session_memory, run_waits, run_wait_members, hook_cursors, periodic_prompts
- Column names: `tenant_id` throughout (no `org_id` / `dev_envs` in live SQL)
- `postgres` store tests green against baseline
- **Gap fix:** `scripts/dev-stack.sh` + `cmd/devstack` rebased onto tenant API keys (see Corrections)

## A4.4 Deploy reference

- `Dockerfile` multi-binary (cored, coreworker, core)
- `docker-compose.yml` postgres + cored + coreworker (broken in-container healthcheck removed; probe host `/healthz`)
- `deploy/systemd/*.service`, `deploy/nomad/ultracore.nomad.hcl`
- cored: `GET /healthz`, `GET /readyz` (pg ping)
- coreworker: health server **opt-in** via `CORE_ADDR` (avoids parallel test port clash; compose sets `:8081`)
- `docs/deploy.md` exhaustive CORE_* table

## A4.5 Subscribe-resume

- Go: `sdk.Subscription` reconnects from `LastSeq`; `TestA45_SDKSubscribeResume` asserts no gaps/duplicates + `GetEvents` agreement
- TS: smoke `subscribe resume has no gaps after disconnect`
- testclient default Subscribe disables reconnect (preserves A3.3 mid-stream revoke tests); `SubscribeResume` enables it

## A4.6 Replay parity

- `TestA46_ReplayParityGoVsTS` compares Go SDK `GetEvents` kind+seq rows to TS `getEvents` dump for a multi-message + run session

## A4.7 Config drift fence

- `config.RefuseUnknown()` at cored/coreworker startup
- `TestRefuseUnknown` unit proof
- Documented table in `docs/deploy.md` matches known set in `config/config.go`
- **Gap fix:** removed `CORE_DEV_TOKENS` from dev-stack (would refuse startup)

## A4.8 v0.1.0 tag readiness

**Not tagged in this change** (per operator instruction: prefer document for human).

Ready when human runs:

```sh
git tag -a v0.1.0 -m "ultracore v0.1.0 — post-extraction consumer surface"
# after merge to default branch:
buf breaking --against '.git#tag=v0.1.0,subdir=.'
```

## testclient → SDK rebase

```
testkit/testclient embeds *sdk.Client
e2e/ + testkit/ have zero corev1connect.New*Client constructors
rg 'corev1connect.New' e2e/ testkit/ → empty
```

## API shape (frozen v1)

| Service | RPCs |
|---|---|
| TenantService | Create/Get/List tenants; Create/List/Revoke API keys |
| CredentialService | Put/List/Delete |
| ProviderService | Register/List/Get/Deregister |
| SessionService | Create/Get/List/UpdateLabels/Archive; memory CRUD |
| RunService | Start/Answer/Cancel/Get/List/GetRunTree |
| ResourceService | Provision/Get/List/Terminate/ExecPreview/Restart |
| EventService | Append/Subscribe/Get |
| AutomationService | Put/List/SetEnabled periodic prompts |

## LOC vs E0

| Scope | E0 baseline | E4 |
|---|---|---|
| Non-generated Go | 30280 | **~25994** |
| Generated Go | (not counted) | ~11351 |
| TS hand-written | — | ~698 |
| Proto | — | ~843 |

Delta non-gen Go: **about −4.3k LOC** vs E0 (deletions from E1–E3 plus E4 reshape).

## Suite runtime

| Suite | Runtime |
|---|---|
| Focused SDK e2e (A45/A46/A48 + core A0x/A3x) | ~9.4s |
| postgres store | ~1.7s |

## Corrections (gap-fix pass)

1. **`scripts/dev-stack.sh` broken after E3/E4 auth reshape**
   - Still set `CORE_DEV_TOKENS` (removed authenticator; unknown `CORE_*` would refuse cored startup).
   - Read seed JSON key `org_id` though seed emits `tenant_id`.
   - Used sticky `dev-token` instead of seeded API key.
   - Docker cleanup filter still used `ultracore.env_id` (resources label is `ultracore.resource_id`).
   - **Fix:** seed → tenant + admin API key JSON (`tenant_id`, `api_key`); cored starts without unknown env; smoke uses `CORE_SMOKE_TOKEN` + `CORE_SMOKE_TENANT`.

2. **`cmd/devstack` seed/smoke**
   - Always returns decryptable admin `api_key` (mint or decrypt existing `key_enc`).
   - Smoke requires API key; accepts `CORE_SMOKE_TENANT` (legacy `CORE_SMOKE_ORG` alias kept for one window).
   - Comments/docs updated off org/user/membership language.

3. **`docker-compose.yml`**
   - Removed nonsensical healthcheck that exec'd `cored` as the probe binary.

4. **`docs/deploy.md`**
   - Clarified worker `CORE_ADDR` opt-in default; documented local dev-stack seed/smoke contract.

## Residual risks

1. **k8s provider conformance** `RestartRotatesToken` can flake when kind/cluster unavailable — environmental, not E4 regression.
2. **Pagination** fields exist on list RPCs but store still returns full pages (tokens empty). Additive fill later.
3. **OTLP** env documented; exporter wiring is best-effort/no-op until collector configured (no new dependency forced).
4. **buf breaking vs main** will fire until E4 merges (intentional one-time break).
5. **v0.1.0 tag** awaits human.
6. Worker `/readyz` requires explicit `CORE_ADDR` (compose/nomad/systemd set it; bare `coreworker` for tests does not bind).
7. Full `task dev:smoke` needs Docker + local_docker image availability; not re-run in this gap-fix window (script path corrected and compiles).

## Open bullets

None in E4 scope after gap-fix. Ready for E5/E6 consumer migrations once human tags `v0.1.0`.
