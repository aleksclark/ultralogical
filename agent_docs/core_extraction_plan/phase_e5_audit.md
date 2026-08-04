# Phase E5 completion audit

Date: 2026-08-03
Branch: `aleks/phase-e5-admin-api`
Reviewer pass: independent verification + isolation fix (AdminStore split)

## Acceptance criteria

| ID | Status | Evidence |
|---|---|---|
| A5.1 | **PASS** | Separate `cmd/coreadmin`, `proto/admin/v1`, `adminhttp/`, `admin/store/`, `admin/query/`, `gen/go/admin`, `clients/admin-ts`. Public `@ultracore/client` exports only `core/v1`. `scripts/check-admin-isolation.sh` rejects admin under `clients/ts`, consumer imports, core protos, **and** `go list -deps ./cmd/cored`. `TestCoredHasNoAdminRoutes`, `TestCoredPackageDepsExcludeAdmin`. Admin store is **not** in package `postgres` so cored cannot link admin symbols. |
| A5.2 | **PASS** | `TestAdminAuthFailClosed` (missing/wrong token → Unauthenticated). `TestCoredHasNoAdminRoutes`. Production refuses boot without token (`cmd/coreadmin`). |
| A5.3 | **PASS** | `phase_e5_inventory.md` maps every baseline table. `hook_cursors` documented exception. River jobs + runtime health included. |
| A5.4 | **PASS** | Max limit 250 enforced. Signed cursors. Concurrent insert traversal. `ListRelated` is bounded first-page only (documented; deep lists use filtered `List*`). |
| A5.5 | **PASS** | Invalid field/op rejected before SQL. Search+filter composition. `DescribeCollection`. |
| A5.6 | **PARTIAL → acceptable** | First-page latency guard on 5k events. Full 100k deferred to local bench (documented). |
| A5.7 | **PASS** | List summaries carry byte counts/previews; full payloads only on Get* detail/blob RPCs. |
| A5.8 | **PASS** | `TestAdminSecretNonDisclosure` with `harness.CanaryAPIKey`. Selects never return `key_enc`/`enc_payload`/`token_enc` plaintext. |

## Work items

| ID | Status |
|---|---|
| T5.1 Admin process/isolation | Done (plus binary dep fence) |
| T5.2 Admin proto + clients | Done |
| T5.3 Query engine | Done |
| T5.4 Read inventory | Done |
| T5.5 Timeline/relationships | Done |
| T5.6 Tests/docs | Done |

## Isolation fix in review

Previously `postgres.AdminStore` lived in package `postgres`, so `cored`
transitively depended on `admin/query` and `gen/go/admin/v1` even though it
never mounted routes. Store moved to `admin/store`; isolation script and
`TestCoredPackageDepsExcludeAdmin` guard the fence.

## Verdict

Phase E5 is **complete enough to start E6**. Query foundation is stable; no SPA
screen should need bespoke list mechanics beyond `SearchRequest` + descriptors.
`ListRelated` is intentional first-page navigation only.
