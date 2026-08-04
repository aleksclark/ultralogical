# Phase E3 completion audit

**Phase:** E3 — Tenancy, identity, labels, and policy hook  
**Branch:** `core-extraction`  
**Date:** 2026-08-03  
**Auditor:** implementer self-audit against phase_e3.md acceptance  
**Review pass:** adversarial review corrected StartRun Actor capture,
Subscribe mid-stream key recheck, `policyFromProto` empty-kinds semantics,
CLI tenant/key verbs, e2e strength, and docs/security ChildInherit drift.

## A3.1 Full suite + fences

| Check | Evidence |
|---|---|
| Unit/store/http/secrets | `go test . ./postgres/ ./http/ ./secrets/` → ok |
| Policy unit | `TestRunPolicy*` → ok |
| Fences E1+E2+E3 | `scripts/check-extraction-fences.sh` → clean |
| Build | `go build ./...` → ok |
| Functional E3 | `TestA31/A33/A34/A35/A36`, `TestA03`, `TestA89` → ok |

## A3.2 Cross-tenant invisibility

`e2e/tenancy_test.go` `TestA31_CrossTenantInvisibility`:

- Bob GetSession(Alice's id) → NotFound
- Bob ListSessions with Alice's label selector → empty list, no error
- Bob Subscribe(Alice's session) → error on stream
- Bob ListCredentials / ListProviders against Alice's tenant id → denied/not-found

Handlers use `requireTenant` / `resolveSessionTenant` collapsing missing and cross-tenant.

## A3.3 Key lifecycle

`TestA33_KeyLifecycle` + `postgres.TestAPIKeyAuth`:

- Sessions-scope key creates sessions; cannot CreateAPIKey / CreateTenant
- After RevokeAPIKey, subsequent GetSession → Unauthenticated
- Open Subscribe re-authenticates on each delivery and on a 1s idle tick;
  revoked keys terminate the stream (A3.3 mid-stream fail-closed)
- Keys stored as SHA-256 + AES-GCM; raw returned once; redactor registered at mint
- e2e asserts `secrets.DefaultRedactor.Redact` scrubs the raw key

CLI verbs: `core tenant create`, `core tenant key create|list|revoke`.

## A3.4 Actor attribution

`TestA34_ActorAttribution`:

- Client sends `X-Core-Actor: student/jacob/...`
- Appended user_message event carries `kind=student`, `id=jacob`
- **StartRun stores caller Actor on the run row** (and proto `actor_kind` /
  `actor_id`); children inherit `run.Actor` at spawn
- Loop-internal events use `ActorAgent(runID)` / `ActorSystem()`

## A3.5 Labels

`TestA35_LabelsCRUDAndSelectors`:

- Create with labels; equality selector; `in` selector
- UpdateSessionLabels emits `SessionLabelsChanged` in subscribe

**Benchmark** (`BenchmarkListSessionsByLabel`, 10k sessions ×8 labels, GIN jsonb_path_ops):

| Metric | Value |
|---|---|
| p95 | **~2.2 ms** (2233 µs reported) |
| Hardware | AMD Ryzen 9 7950X |
| Gate | < 50 ms p95 — **pass** |

## A3.6 Policy

`TestA36_PolicyEnforcement` + `TestA89_SecurityDocumentation` + unit tests:

- Denied tool → uniform `permission_denied` event
- Empty ResourceKinds denies provision at type level **and** is preserved
  through `policyFromProto` (no silent widen to `*`)
- Child tools shorthand narrows AllowTools; `IsSubset` refuses escape
- `ChildInherit` forces parent policy when set (e2e subtest)
- MaxChildren enforced across concurrent spawn attempts (e2e subtest)
- DefaultRunPolicy: tools=`*`, kinds=`*`, MaxChildren=16, **ChildInherit=false**

### Interim E1 allowlist absorbed

| Before (E1) | After (E3) |
|---|---|
| `Grants{Tools}` | `RunPolicy{AllowTools,DenyTools,...}` |
| `DefaultGrants()` | `DefaultRunPolicy()` |
| `grants` JSON column | same column, new JSON shape; `decodePolicy` accepts legacy `tools` |
| Dual path | **none** — only `run.Policy.AllowsTool` |

Confirmed: no `type Grants`, no `DefaultGrants`, no dual dispatch path in live code (fence).

### Lattice → policy mapping

| Old lattice guarantee | Status |
|---|---|
| Flat tool allowlist | **Survived** as `AllowTools` + `DenyTools` |
| Existence-oracle denial stubs | **Survived** (`CanonicalTools` + denial stubs) |
| Child inherit of tools when omitted | **Survived** (nil tools → parent allow) |
| Explicit empty tools = mute | **Survived** |
| `env_all` / per-env authority | **Dropped** (E1) |
| `MaySpawn` boolean | **Survived** as `MaxChildren > 0` |
| `MaxChildren` monotone | **Survived** as subset MaxChildren ≤ parent |
| General `SubsetOf` lattice | **Collapsed** to three-axis `IsSubset` |
| `RootGrants` | **Dropped** (E1) |
| Cohort grants | **Dropped** (E1); cohort uses spawn policy path |

## A3.7 Two-tenant provider isolation

Structural rename only: `stack.TenantA`/`TenantB`, `store.Tenant(id)`.  
Provider e2e paths updated to Tenant naming. Isolation mechanism unchanged from E2
(per-tenant provider instances + scoped resource store). Re-proven by existing
provider tests under new names once full suite is green.

## Residual risks

1. **DB column still `org_id`** until E4 squash — domain is Tenant; SQL keeps historical name. Job JSON tags also retain `org_id` for queue payload compatibility.
2. **gopls cache** may show stale diagnostics; `go build`/`go test` are authoritative.
3. **Subscribe mid-stream revoke** now rechecks the key on every event delivery and on a 1s idle ticker. Delivery latency between revoke and close is bounded by that tick when the bus is idle.
4. **CreateTenant requires admin key** — first key still needs seed (harness/devstack/CLI ops).
5. **Proto3 max_children=0** cannot be distinguished from unset when tools are non-empty; handler applies DefaultRunPolicy's spawn cap. Mute policies (empty allow_tools) preserve MaxChildren=0.

## Deliverables checklist

- [x] Implementation (domain, store, http, loop, protos, testkit, e2e)
- [x] `fences/e3.txt`
- [x] `phase_e3_inventory.md`
- [x] `phase_e3_audit.md` (this file)
- [x] `docs/security.md` rewrite
- [x] `AGENTS.md` iron rule 2 → Tenant
- [x] Benchmark numbers recorded
- [x] CLI `tenant create|key create|list|revoke`
- [x] No commit/push (per instructions)
