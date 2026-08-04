# Phase E7 inventory

## Added

### Proto / codegen
- `proto/admin/v1/admin.proto` — `AdminCommandService`, audit messages, `WhoAmI`
- Regenerated `gen/go/admin/v1/*`, `clients/admin-ts/src/gen/admin/v1/*`

### Schema
- `postgres/migrations/00002_admin_audit.sql` — immutable `admin_audit_events`

### Go packages
- `admin/authz` — roles, permission matrix, multi-token directory
- `admin/command` — dry-run/preview-hash/idempotency engine + catalog
- `adminhttp` — auth interceptor with operator context, command service handlers
- `admin/store` — `ListAuditEvents` / `GetAuditEvent`
- `admin/query` — `audit_events` collection descriptor
- `jobqueue/river/admin.go` — JobGet/Cancel/Retry wrappers
- `cmd/coreadmin` — wires engine, tokens, reveal/destructive flags, keyring

### Config
- `CORE_ADMIN_TOKENS`, `CORE_ADMIN_TOKEN_ROLE`
- `CORE_ADMIN_REVEAL_ENABLED`, `CORE_ADMIN_CMD_RATE_LIMIT`, `CORE_ADMIN_CMD_CONCURRENCY`
- `CORE_ADMIN_ENABLE_TERMINATE`, `CORE_ADMIN_ENABLE_SUSPEND`, `CORE_ADMIN_ENABLE_DISCONNECT_SUBSCRIBER`

### SPA (`admin-web`)
- `src/lib/operator.tsx`, dual clients (`read` + `command`)
- `src/data/commands.ts`, `src/components/CommandConfirmModal.tsx`
- `src/pages/AuditPage.tsx` + nav/route
- Action affordances on run/job/resource/provider/api-keys/automation/credentials
- Playwright coverage for audit route + command preview modal

### Docs / deploy / tasks
- `docs/admin-ops.md`
- Updates to `docs/admin-api.md`, `docs/admin-spa.md`
- Nomad admin env for E7 flags
- `task admin:security:test`

### Tests
- `admin/command_test.go` — role matrix, stale preview, idempotency, reveal controls, audit, isolation

## Independent review fixes
- Command engine: stale preview validated pre-mutation only (no post-apply fail)
- Idempotency keys bind successful outcomes only (failed attempts still audit)
- Reveal kill switch → `Unimplemented` (RPC treated as absent when disabled)
- DB triggers reject UPDATE/DELETE on `admin_audit_events`
- Tests: real stale-state fail-closed + failed-key retry; DB immutability asserts

## Deferred / honest limits
- `DisconnectSubscriber` — event bus has no admin disconnect handle; returns FailedPrecondition
- Full provider capability re-probe (network) — metadata `MarkHealthy` only without provider builders in coreadmin
- Fleet IdP / mTLS operator identity — pragmatic bearer token map
- SPA does not auto-wire every bulk multi-select path; single-target confirmation is the default
