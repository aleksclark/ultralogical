# Phase E1 audit — Shed the product surface

**Branch:** `core-extraction`  
**Depends on:** E0 (`5da5f64`)  
**Module after rename:** `github.com/aleksclark/ultracore`  
**Reviewer corrections:** applied in-tree (no commit); see Corrections below.

## Acceptance evidence

### A1.1 — build / lint / tests / codegen / coverage

| Gate | Result | Notes |
|---|---|---|
| `task build` / `go build ./...` | **PASS** | clean under `github.com/aleksclark/ultracore` |
| extraction fences | **PASS** | `scripts/check-extraction-fences.sh` → `extraction fences: clean` |
| `go vet` (loop/cmd/http/envprovider) | **PASS** | |
| `task verify:coverage` | **PASS** | 24 capabilities, 35/35 RPCs covered |
| unit / functional / cli / codegen | **PASS** (implementer) | full suite green before review; focused gates re-checked |

k8s/nomad conformance packages require a live kind/Nomad agent (CI legs
`providers-kubernetes` / `providers-nomad`). Locally they skip or fail without
those control planes; they are not broken by E1 deletions. Hosted isolation
tests (`TestA103_HostedIsolationAndQuota`) were removed with hosted.

### A1.2 — fence grep clean

Active fence file: `agent_docs/core_extraction_plan/fences/e1.txt`

```
flowdef|FlowInvocation|BillingService|env_usage|hosted_eks|Presence|GPUI|Playwright|Org\.Plan|RootGrants|SubsetOf|ultralogical
```

`scripts/check-extraction-fences.sh` → **clean**.  
Migrations remain excluded until the E4 squash (`postgres/migrations/**`),
because historical `env_usage` table DDL still exists in `00003_envs.sql`.

### A1.3 — loop e2e still works

Proven by surviving functional tests:

- provision + bash/view: `TestA22_A23_AgentEnvPersistence`
- spawn + wait + cohort: `TestA81_*`, `TestA83_*`, `TestA82_WaitRaceMatrix`
- flat allowlist denial: `TestE1_FlatAllowlistDenialVisibility`
- replay: `TestA03_ResumeContract`, `TestA84_CrossReplicaSubscription`

### A1.4 — memory + run trees survived

- `TestA86_SessionMemory` (presence subtests removed; memory CRUD/caps/concurrency kept)
- `TestA81_SpawnDurabilityAndGrants`, `TestA83_CohortFanOutFanIn`
- Memory types remain in root package (`multiplayer.go` filename is historical;
  no separate `memory.go` yet — acceptable; inventory said "moves toward")

### A1.5 — periodic prompts survived

- `TestA66_PeriodicPromptAPI` green
- `automation.go` (26-line substrate) untouched

### A1.6 — five providers remain

Kinds kept: `local_docker`, `byo_k8s`, `byo_nomad`, `static`, `tunnel_local`.  
`hosted_eks` deleted. k8s `Probe` restored as BYO-only (no
namespace_isolation / resource_quota). CI provider legs retained minus
hosted isolation assertions.

### A1.7 — module renamed

| Before | After |
|---|---|
| `github.com/aleksclark/ultralogical` | `github.com/aleksclark/ultracore` |
| package `ultra` | package `core` (import alias `uc`) |
| `ultrad` / `worker` / `ultra` | `cored` / `coreworker` / `core` |
| `ULTRA_*` env | `CORE_*` env |
| proto `ultra.v1` | proto `core.v1` |
| `gen/go/ultra/v1` | `gen/go/core/v1` |

`rg 'github.com/aleksclark/ultralogical' --glob '*.go'` → empty (fence term).

Operational rename completed in review: CI/dev-stack Bezalel image tags and
docker leak labels now use `ultracore/*` / `ultracore.env_id` consistently
with `envprovider/localdocker` and the harness.

### A1.8 — tree shrank; coverage clean

| Metric | Value |
|---|---|
| Non-generated Go LOC before (E0 baseline) | ~30,280 |
| Non-generated Go LOC after E1 (review) | ~22,193 |
| Delta | **≈ −8,100 LOC (−27%)** |

`coverage.json` rewritten to Go-only evidence schema; `verify:coverage` green
with no dangling test refs. No web/desktop columns.

## Inventory drop/E1 rows — deleting approach

| Surface | Approach |
|---|---|
| `ui/`, `clients/rust`, Playwright/GPUI e2e | `rm -rf`; Taskfile/CI web+desktop jobs removed |
| Flows (`flow.go`, `flowdef*`, `flowwork/`, flow proto/http/postgres/e2e/docs) | Full delete; spawn keeps explicit per-call agent specs only |
| Billing/metering (`EnvUsage`, `BillingService`, `RateClass`, watermark) | Domain+store+http+envwork paths removed; schema left for E4 squash |
| `Org.Plan` | Field + readers removed; DB column default remains until squash |
| Hosted EKS (`hosted.go`, kind, NetworkPolicy/Quota/CIDR) | Delete; BYO k8s sole k8s adapter; Probe restored without hosted caps |
| Presence / multiplayer humans | Types+handler+reaper deleted; memory RPCs folded into session handler |
| Grants lattice | `SubsetOf`/`EnvAll`/`MaySpawn`/`MaxChildren`/`RootGrants` deleted; flat `Tools []string` allowlist at dispatch; `"*"`=all; nil tools inherit parent, empty tools = none |
| CLI flow verbs | `cmd/core` keeps provider verbs only |
| Module rename | Mechanical pass: go.mod, imports, protos, binaries, env prefix; CI image/label rename finished in review |

## Intentionally weakened (E3 follow-up)

**Grants → flat allowlist.** No monotone narrowing, no env authority, no
max-children. Children inherit parent tools when `tools` is omitted; an
explicit empty list means no tools. E3 adds the consumer policy hook on top.

## Corrections applied during adversarial review

1. **CI/dev-stack Bezalel image tag mismatch** — workflows and
   `scripts/dev-stack.sh` still built/used `ultralogical/bezalel:phase2-test`
   while harness/providers use `ultracore/bezalel:phase2-test`. Fixed to
   `ultracore/...` everywhere operational.
2. **Docker leak label mismatch** — CI and dev-stack filtered
   `ultralogical.env_id` but containers are labelled `ultracore.env_id`.
   Fixed filters so leak checks and smoke cleanup actually work.
3. **`TestA78_DevStackSmoke` still required `"usage interval(s)"`** after
   metering deletion; smoke no longer prints it. Assertion removed.
4. **Onboarding guide + runner** still documented/parsed `ultra` CLI and
   `ultralogical-envs` / `managed-by=ultralogical`. Rewrote guide to `core`
   CLI + `ultracore-envs` / `ultracore`; updated
   `cmd/core/cli/onboarding_test.go` accordingly.
5. **`docs/security.md` still described the grants lattice**
   (`env_all`/`may_spawn`/`max_children`/`RootGrants`/`SubsetOf`) and
   referenced deleted subtests. Rewrote to flat allowlist + surviving
   TestA89 claims.
6. **`agent_docs/providers.md` / `docs/providers.md` still documented
   hosted isolation, hosted capabilities, and `ULTRA_*` env.** Rewrote for
   five surviving kinds and `CORE_*`.
7. **`README.md`, `agent_docs/architecture.md`, `dev_environments.md`,
   `testing.md`, `conventions.md`, `cross_client_testing.md`** still claimed
   first-party web/desktop, flows, presence, billing, GPUI/Playwright
   evidence. Rewrote to post-E1 substrate framing.
8. **TS package name** `@ultralogical/client` → `@ultracore/client`.
9. **Dead `flowwork` ban entry** left in CLI public-API test (harmless but
   product residue). Removed.
10. **Stale comments** (metering test name comment, max_children spawn
    comments, CORS "browser first-class", binary headers, OrgID "billing
    boundary"). Cleaned.

## Remaining risks

1. **Migrations still contain product tables** (`env_usage`, `flows`,
   `participants`, `plan` column). Code no longer reads them; E4 squash
   drops them from the baseline schema.
2. **k8s/nomad conformance** need live clusters (CI). Probe was restored after
   hosted.go deletion — verify CI kind leg still green with new image tag.
3. **Import alias `uc`** for root package `core` avoids variable shadowing;
   follow this convention in new code.
4. **TS smoke env** is `CORED_URL` / `CORE_TOKEN` / `CORE_ORG_ID`.
5. **Historical filenames** (`ultra.go`, `multiplayer.go`, harness
   `UltradReplicas`/`RestartUltrad`) remain; package/binary names are correct.
   Optional cleanup later — not E1 acceptance blockers.
6. **No commit** was made (per instructions).

## Open scoped bullets

None after corrections. E1 is ready to close → E2.
