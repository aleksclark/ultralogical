# Private admin SPA (`admin-web`)

The admin SPA is a **private** React console over `admin.v1` only (read + gated commands).
It is not mounted on `cored`, not published on the public edge, and must never
import `@ultracore/client` or call `core.v1`.

## Stack

- React + Vite + TypeScript + Tailwind
- Hand-rolled shadcn-style dark UI primitives
- Generated Connect-ES client from `clients/admin-ts` (symlinked as `admin-web/src/gen`)
- `@tanstack/react-virtual` for within-page virtualization

## Auth

Operators paste `CORE_ADMIN_TOKEN` on `/login`.

| Store | Allowed? |
|---|---|
| In-memory | yes (default) |
| `sessionStorage` (tab lifetime) | optional checkbox |
| `localStorage` | **never** |
| URL / cookies | **never** |
| Tenant API keys (`uck_…`) | **rejected** |

Token is sent as `Authorization: Bearer …` on every admin Connect call.

## Talking to coreadmin

**Documented path for local + Playwright:** Vite proxy.

- Browser origin: SPA (`http://127.0.0.1:<spa-port>`)
- API: same origin `/admin.v1.*` → proxied to `CORE_ADMIN_URL` (default `http://127.0.0.1:8082`)
- Config: `admin-web/vite.config.ts` (`server.proxy` + `preview.proxy`)

Optional: set `VITE_ADMIN_API_URL` to a separate admin origin and configure
`CORE_ADMIN_CORS_ORIGIN` on `coreadmin`. Prefer the proxy for e2e.

## Architecture

### Query state (URL-backed)

Every collection serializes into the URL:

- `q` search
- `f` filters (`field:op:value`)
- `s` sorts (`-field` = desc)
- `cursor` / `cstack` keyset pagination
- `limit` (default 50, max 250)
- `tenant` explicit tenant scope
- `cols` visible columns
- `view` saved view id
- `detail` drawer selection

Back/forward/refresh/deep-link restore the same result set. Changing
search/filter/sort/limit/tenant resets the cursor; column/view changes do not
refetch.

### Shared primitives

| Primitive | Role |
|---|---|
| `useQueryState` | typed URL state |
| `CollectionPage` | list composition |
| `AdminDataTable` | virtualized rows, keyboard nav |
| `SearchBar` | debounced + cancelable via AbortSignal |
| `FilterBuilder` | metadata-driven chips |
| `CursorPager` | prev/next + page size |
| `SavedViews` | localStorage presets per browser |
| `DetailDrawer` / `EntityLink` | inspect + correlate |
| `JsonViewer` | bounded, searchable, downloadable |
| `QueryBoundary` | loading/empty/error/stale |

### Data layer (mandatory)

**Only** `src/data/collections.ts` may call `List*` RPCs.
Routes use `useCollection` / `listCollection`. CI gate:

```sh
task admin:web:gate
# or: (cd admin-web && node scripts/check-import-gates.mjs)
```

Also fails on `@ultracore/client` / `core.v1` imports.

## Routes

| Path | Purpose |
|---|---|
| `/` | Runtime health, counts, problem-view links |
| `/tenants`, `/tenants/:id` | Tenant inventory + related first pages |
| `/sessions`, `/sessions/:id` | Sessions + diagnostic timeline |
| `/events` | Global/session events + payload drawer |
| `/runs`, `/runs/:id` | Runs, steps, on-demand history blob |
| `/resources`, `/resources/:id` | Resources + provider/run links |
| `/providers`, `/providers/:id` | Providers + resource links |
| `/jobs`, `/jobs/:id` | River jobs |
| `/automation` | Periodic prompts |
| `/memory` | Session memory entries |
| `/waits` | Spawn/wait and tool await state |
| `/credentials` | Ciphertext metadata only |
| `/api-keys` | Key metadata / revocation only |
| `/security` | Actor search across events/runs |
| `/audit` | Immutable operator command audit |
| `/internals` | Build/schema/descriptors/raw health |

## How to run

```sh
# Install once
task admin:web:install   # or: (cd admin-web && npm install)

# Against a running coreadmin (proxy default :8082)
(cd admin-web && CORE_ADMIN_URL=http://127.0.0.1:8082 npm run dev)
# open http://127.0.0.1:5173  → paste CORE_ADMIN_TOKEN

# Production build
task admin:web:build

# Full Playwright against real Postgres + coreadmin + SPA
task admin:web:test
```

`task admin:web:test` runs `scripts/admin-e2e-stack.sh spa`, which:

1. Boots disposable Postgres + `coreadmin` and seeds fixtures (including failed run / stuck resource for golden paths).
2. Builds the SPA and serves it with `vite preview`, proxying `/admin.v1.*` to coreadmin.
3. Runs Playwright golden workflows + route smokes + secret/network gates.

## Performance rules

- Page size default 50, hard max 250 (server-enforced).
- Virtualize within the current page only; prior pages are not retained unless the operator pins via saved views / URL.
- Search changes abort in-flight requests; stale responses are dropped.
- Large payloads (run history, full event JSON) load on demand in drawers/detail.

## Security

- `noindex` meta; private deployment only (no public service discovery).
- Secrets (API key plaintext, credential plaintext, resource tokens) never render.
- Canary secret checks in Playwright scan DOM + storage.

## Ops (E7)

- Role-aware command buttons on detail pages; confirmation modal (preview → execute)
- Break-glass reveal modal with reauth + reason; no auto clipboard / localStorage
- Audit page lists `admin_audit_events` via collection primitives
- Commands live in `src/data/commands.ts` (not the List* data layer)

## Non-goals

- Server-side saved views
- Mounting SPA on `cored`
- Generic bulk mutation without typed confirmation
