# Phase E6 inventory — E5 surface → SPA route mapping

| E5 collection / RPC | SPA route | List via data layer | Detail | Notes |
|---|---|---|---|---|
| `ListTenants` / `GetTenant` | `/tenants`, `/tenants/:id` | `collections.ts` → `listTenants` | dedicated page | Related sessions/keys/providers first page |
| `ListAPIKeys` / `GetAPIKey` | `/api-keys` | `listAPIKeys` | drawer | Metadata only |
| `ListSessions` / `GetSession` | `/sessions`, `/sessions/:id` | `listSessions` | dedicated + timeline | `GetSessionTimeline` on detail |
| `ListEvents` / `GetEvent` | `/events` | `listEvents` | drawer | Global + session filter |
| `ListRuns` / `GetRun` / `GetRunHistory` | `/runs`, `/runs/:id` | `listRuns` | dedicated | History blob on demand |
| `ListRunSteps` | `/runs/:id` (embedded) | `listRunSteps` | table on run detail | Filtered by `run_id` |
| `ListResources` / `GetResource` | `/resources`, `/resources/:id` | `listResources` | dedicated | Provider/run deep links |
| `ListProviders` / `GetProvider` | `/providers`, `/providers/:id` | `listProviders` | dedicated | Resource filter links |
| `ListCredentials` / `GetCredential` | `/credentials` | `listCredentials` | drawer | Ciphertext length only |
| `ListPeriodicPrompts` / `GetPeriodicPrompt` | `/automation` | `listPeriodicPrompts` | drawer | |
| `ListMemory` / `GetMemory` | `/memory` | `listMemory` | drawer | Also linked from session detail |
| `ListWaits` / `GetWait` | `/waits` | `listWaits` | drawer | Also linked from run/session detail |
| `ListJobs` / `GetJob` | `/jobs`, `/jobs/:id` | `listJobs` | dedicated | River; empty if schema absent |
| `GetRuntimeHealth` | `/`, `/internals` | detail helper | — | Counts + queue depths |
| `DescribeCollection` | `/internals` | detail helper | — | Descriptor dump |
| `ListRelated` | tenant/session detail | detail helper | — | First-page only |
| `GetSessionTimeline` | `/sessions/:id` | detail helper | — | Diagnostic timeline |
| Security diagnostics | `/security` | events+runs via data layer | actor search | Audit log deferred E7 |
| `hook_cursors` | — | — | — | Deferred (E5); not required |

## Query primitives

| Primitive | Path |
|---|---|
| Query state | `admin-web/src/query/*` |
| Collection data layer | `admin-web/src/data/collections.ts` |
| `useCollection` | `admin-web/src/data/useCollection.ts` |
| UI primitives | `admin-web/src/components/*` |
| Import / list-RPC gate | `admin-web/scripts/check-import-gates.mjs` |
| Playwright SPA e2e | `admin-web/e2e/spa.spec.ts` |
| Stack bootstrap | `scripts/admin-e2e-stack.sh spa` |

## Golden workflows

| # | Workflow | Entry |
|---|---|---|
| 1 | failed run → steps → jobs/resources/waits | `/runs?f=state:eq:failed` → `/runs/:id` |
| 2 | session event → actor → run | `/events` drawer → actor filter (URL `detail=` round-trip) |
| 3 | stuck resource → provider → jobs | `/resources?f=state:eq:failed` → detail → provider |
| 4 | cross-tenant same query | `?tenant=` banner on lists |
| 5 | latency / oldest jobs | overview → jobs presets |

## Deferred

| Item | Why |
|---|---|
| Server-side saved views | localStorage per browser (documented shortcut) |
| Storybook | Playwright covers via real pages |
| axe CI gate | Manual a11y labels present; automated axe optional follow-up |
| Full 100k DOM memory bench in CI | Bounded page + virtualization enforced; scale seed local |
| E7 mutations / audit log / break-glass | Explicitly out of scope |
