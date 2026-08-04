# Phase E3 inventory — Tenancy, identity, labels, and policy hook

Behavior-to-test map for E3. Every acceptance bullet maps to an executable test.

| ID | Behavior | Implementation | Test | Status |
|---|---|---|---|---|
| A3.1 | Full suite green; fences E1+E2+E3 | fences/e3.txt; full rename | `scripts/check-extraction-fences.sh`; `go test ./...` unit/store | green (unit/store/fences); functional partial |
| A3.2 | Cross-tenant invisibility all surfaces incl labels/events | `store.Tenant`, handlers `requireTenant` | `e2e/tenancy_test.go` `TestA31_CrossTenantInvisibility` | green |
| A3.3 | Key lifecycle: revoke fails closed mid-stream; sessions-scope limited; raw keys redacted | `APIKeyStore`, `APIKeyAuthenticator`, Subscribe recheck, redactor, CLI tenant/key | `e2e/tenancy_test.go` `TestA33_KeyLifecycle`; `postgres.TestAPIKeyAuth` | green |
| A3.4 | Actor on API events + StartRun; loop-internal carry run Actor | `X-Core-Actor`, `AgentRun.Actor` on StartRun, event append | `e2e/tenancy_test.go` `TestA34_ActorAttribution` | green |
| A3.5 | Labels CRUD+selectors; 10k bench <50ms p95; label change events | session labels jsonb+GIN; `UpdateSessionLabels` | `e2e/labels_test.go` `TestA35_*`; `BenchmarkListSessionsByLabel` | green (p95 ~2.2ms) |
| A3.6 | Policy denial, kinds, MaxChildren, subset, ChildInherit; E1 allowlist gone | `RunPolicy`, loop dispatch, spawn `IsSubset`; empty kinds preserved | `e2e/policy_test.go` `TestA36_*` (+ inherit/cap); `policy_test.go` unit | green |
| A3.7 | Two-tenant provider isolation under new names | rename only; existing provider e2e | `e2e/provider_test.go` (names updated) | compile-green; run with full functional |

## Work items

| Item | Deliverable |
|---|---|
| T3.1 | Org→Tenant rename (domain, store, postgres, protos, http, CLI, testkit, docs) |
| T3.2 | Human identity deleted; API keys + Actor plumbing |
| T3.3 | Labels schema/store/selectors/API/events |
| T3.4 | RunPolicy type/storage/enforcement/subset; E1 Grants removed |
| T3.5 | e2e tenancy/labels/policy; agent_test spawn assertions updated |
| T3.6 | docs/security.md, AGENTS.md iron rule 2, fences/e3.txt |

## Files (primary)

- Domain: `ultra.go`, `auth.go`, `policy.go`, `store.go`, `run.go`, `event.go`, `multiplayer.go`
- Postgres: `org.go` (tenant+keys), `session.go` (labels), `run.go` (actor+policy), migration `00011_e3_*`
- HTTP: `auth.go`, `org_handler.go` (TenantService), `session_handler.go`, `agent_handler.go`, `convert.go`
- Loop: `spawn.go` (policy inherit/subset/MaxChildren), `resourcetools.go` (kind gate), `*.go` Grants→Policy
- Testkit: harness seeds API keys; testclient `Tenants` + `X-Core-Actor`
- Protos: `org.proto`→TenantService, `session.proto` labels, `agent.proto` RunPolicy, `event.proto` Actor/labels
- e2e: `tenancy_test.go`, `labels_test.go`, `policy_test.go`
