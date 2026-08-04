# Phase E5 completion audit

Date: 2026-08-03
Branch: `aleks/phase-e5-admin-api`

## Acceptance criteria

| ID | Status | Evidence |
|---|---|---|
| A5.1 | **PASS** | Separate `cmd/coreadmin`, `proto/admin/v1`, `adminhttp/`, `gen/go/admin`, `clients/admin-ts`. Public `@ultracore/client` exports only `core/v1`. Codegen fence rejects admin under `clients/ts/src/gen/admin`. `TestCoredHasNoAdminRoutes`. |
| A5.2 | **PASS** | `TestAdminAuthFailClosed` (missing/wrong token → Unauthenticated). `TestCoredHasNoAdminRoutes`. Production refuses boot without token (`cmd/coreadmin`). |
| A5.3 | **PASS** | `phase_e5_inventory.md` maps every baseline table. `hook_cursors` documented exception. River jobs + runtime health included. |
| A5.4 | **PASS** | Max limit 250 enforced. Signed cursors. Concurrent insert traversal. |
| A5.5 | **PASS** | Invalid field/op rejected before SQL. Search+filter composition. `DescribeCollection`. |
| A5.6 | **PARTIAL → acceptable** | First-page latency guard on 5k events. Full 100k deferred to local bench (documented). |
| A5.7 | **PASS** | List summaries carry byte counts/previews; full payloads only on Get* detail/blob RPCs. |
| A5.8 | **PASS** | `TestAdminSecretNonDisclosure` with `harness.CanaryAPIKey`. |

## Work items

| ID | Status |
|---|---|
| T5.1 Admin process/isolation | Done |
| T5.2 Admin proto + clients | Done |
| T5.3 Query engine | Done |
| T5.4 Read inventory | Done |
| T5.5 Timeline/relationships | Done |
| T5.6 Tests/docs | Done |

## Verdict

Phase E5 is **complete enough to start E6**. Query foundation is stable; no SPA
screen should need bespoke list mechanics beyond `SearchRequest` + descriptors.
