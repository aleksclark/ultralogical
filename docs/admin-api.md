# Private admin API (`coreadmin`)

The admin API is a **separate trust boundary** from the consumer `core.v1` API.
It exists so operators can debug ultracore across tenant boundaries without
elevating tenant API keys or expanding the public surface.

## Separation

| Surface | Binary | Protos | Auth | Bind default |
|---|---|---|---|---|
| Consumer | `cored` | `proto/core/v1` | tenant API keys (`uck_…`) | `:8080` |
| Admin | `coreadmin` | `proto/admin/v1` | operator bearer (`CORE_ADMIN_TOKEN`) | `127.0.0.1:8082` |

Hard rules:

- `cored` never mounts admin routes.
- Admin messages never appear in `proto/core/v1` or `@ultracore/client`.
- The private TS client is generated into `clients/admin-ts/` for the E6 SPA.
- Admin requests do **not** inherit a tenant scope. `tenant_id` is an explicit filter.
- CORS is disabled unless `CORE_ADMIN_CORS_ORIGIN` names the admin SPA origin.
- Startup refuses to run without `CORE_ADMIN_TOKEN` unless `CORE_ADMIN_DEV_MODE=true`.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | required | Postgres |
| `CORE_ADMIN_ADDR` | `127.0.0.1:8082` | Listen address (private by default) |
| `CORE_ADMIN_TOKEN` | required* | Operator bearer token |
| `CORE_ADMIN_DEV_MODE` | `false` | Local-only escape hatch |
| `CORE_ADMIN_CORS_ORIGIN` | empty | Single SPA origin; empty disables CORS |
| `CORE_ADMIN_CURSOR_SECRET` | ephemeral | HMAC secret for opaque cursors |
| `CORE_ADMIN_TOKEN_ROLE` | `admin` | Role for single `CORE_ADMIN_TOKEN` |
| `CORE_ADMIN_TOKENS` | empty | JSON map token→`{role,name,id}` |
| `CORE_ADMIN_REVEAL_ENABLED` | `false` | Break-glass reveal kill switch |
| `CORE_ADMIN_ENABLE_TERMINATE` | `false` | Allow ResourceTerminate |
| `CORE_ADMIN_ENABLE_SUSPEND` | `false` | Allow ResourceSuspend |
| `CORE_ADMIN_CMD_RATE_LIMIT` | `20` | Command executes per second |
| `CORE_ADMIN_CMD_CONCURRENCY` | `8` | Max in-flight commands |
| `CORE_MASTER_KEY` | empty | Optional; required for reveal decrypt |
| `CORE_MIGRATE` | `true` | Apply goose migrations on boot |

\*required unless `CORE_ADMIN_DEV_MODE=true`.

## Query envelope

Every list/search RPC uses:

```text
SearchRequest { query, filters[], sorts[], page { limit, cursor } }
PageInfo { next_cursor, has_more }
```

Rules:

- Keyset pagination (not offset). Default limit 50, maximum 250.
- Cursors are opaque, HMAC-signed, bound to collection + query fingerprint, and expire.
- Filters/sorts are allowlisted per collection (`DescribeCollection`).
- Invalid fields/operators fail with `InvalidArgument` **before** SQL construction.
- Cost limits: max 16 filters, 4 sorts, 256-char query, 5s statement timeout.
- List responses are summaries only. Large payloads use detail/blob RPCs
  (`GetEvent`, `GetRun`, `GetRunHistory`, `GetResource`, …).

## RPCs

`AdminReadService` (ConnectRPC):

- `DescribeCollection`
- `ListTenants` / `GetTenant`
- `ListAPIKeys` / `GetAPIKey` (metadata only — never raw keys)
- `ListSessions` / `GetSession`
- `ListEvents` / `GetEvent`
- `ListRuns` / `GetRun` / `GetRunHistory`
- `ListRunSteps`
- `ListResources` / `GetResource`
- `ListProviders` / `GetProvider`
- `ListCredentials` / `GetCredential` (ciphertext metadata only)
- `ListPeriodicPrompts` / `GetPeriodicPrompt`
- `ListMemory` / `GetMemory`
- `ListWaits` / `GetWait`
- `ListJobs` / `GetJob` (River)
- `GetRuntimeHealth`
- `GetSessionTimeline`
- `ListRelated` (first-page relationship navigation; deep lists use filtered `List*`)
- `WhoAmI` (operator id/role/permissions)
- `ListAuditEvents` / `GetAuditEvent`

`AdminCommandService` (ConnectRPC) — typed mutations with dry-run/preview hash/
idempotency/audit. See [`docs/admin-ops.md`](./admin-ops.md).

Admin store code lives in `admin/store` (not `postgres/`) so the `cored`
binary never links admin protos or the query engine.

## Secrets

Ordinary admin reads never return:

- tenant API key plaintext or full key material
- credential plaintext
- resource token plaintext

Visible metadata may include prefixes, hash prefixes, ciphertext byte lengths,
and redaction status. Break-glass reveal is `RevealSecret` behind
`CORE_ADMIN_REVEAL_ENABLED` (default off); see admin-ops.md.

## Deployment

Nomad includes an optional `admin` group with **no Traefik tags**. Keep
`coreadmin` on a private network path (VPN / internal mesh). Do not publish it
on the public edge.

## Tests

Go functional suite (httptest + real Postgres via testcontainers):

```sh
task admin:test
```

Playwright API-level e2e (real `coreadmin` process + disposable Postgres;
no SPA):

```sh
task admin:e2e
```

What `task admin:e2e` does:

1. Starts Postgres in Docker and builds `coreadmin` (and `cored` for isolation checks).
2. Boots `coreadmin` with `CORE_ADMIN_TOKEN`, migrates, and seeds tenants / keys /
   credentials / sessions / events / runs.
3. Writes an endpoint JSON file (`admin_url`, `admin_token`, optional `cored_url`,
   `canary_api_key`).
4. Runs `admin-e2e` Playwright tests via `APIRequestContext` and the generated
   `@ultracore/admin-client` Connect client (`@connectrpc/connect`).
5. Tears the stack down.

Coverage includes: auth fail-closed, `/healthz`+`/readyz`, `ListTenants`
pagination, `limit > 250` rejected, `DescribeCollection`, list smokes for
sessions/events/runs, `GetRuntimeHealth`, secret non-disclosure, and cored 404
on admin paths when both URLs are available.

Manual / debug:

```sh
# Install deps once
(cd admin-e2e && npm ci)

# Boot stack and keep it (prints endpoint JSON path; Ctrl-C to stop)
bash scripts/admin-e2e-stack.sh boot

# Re-run Playwright against an existing endpoint JSON
bash scripts/admin-e2e-stack.sh run /path/to/endpoints.json

# Or point tests at an already-running coreadmin
ADMIN_E2E_URL=http://127.0.0.1:8082 ADMIN_E2E_TOKEN=… ADMIN_E2E_CORED_URL=http://127.0.0.1:8080   (cd admin-e2e && npx playwright test)
```

Scale note: CI seeds 5k events for pagination correctness and first-page
latency. A bulk-insert path in the scale test can be raised toward 100k locally
when benchmarking indexes.

## Admin SPA (E6)

See [`docs/admin-spa.md`](./admin-spa.md). The private React console lives in
`admin-web/` and talks only to this API (Vite proxy or `CORE_ADMIN_CORS_ORIGIN`).

```sh
task admin:web:test   # Playwright SPA against real coreadmin + seeded Postgres
```

## Non-goals

- No generic SQL/shell mutation primitive
- No changes to consumer `core.v1` semantics
- SPA is never mounted on `cored`

