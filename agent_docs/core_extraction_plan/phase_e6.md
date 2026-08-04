# Phase E6 — Comprehensive admin SPA

**Objective:** Build a private, comprehensive React admin SPA over the E5
admin API. Operators can search, page, inspect, correlate, and deep-link every
internal data family without loading thousands of rows into the browser.

**Depends on:** E5.

---

## Scope

- `admin-web/`: React + Vite + TypeScript + Tailwind + shadcn/ui, dark mode.
- Uses only the generated `admin.v1` client. It must not import
  `@ultracore/client` or call `core.v1`.
- Read-only operational exploration. Mutations and break-glass controls are E7.
- Responsive desktop-first UI; keyboard-accessible and deep-linkable.

## Foundational UI architecture

Pagination and search are product primitives, not per-screen widgets:

### Query state

A single typed query-state model represents:

- free-text search;
- typed filter chips;
- sort columns/direction;
- cursor history and page size;
- optional tenant/time-range scope;
- visible columns and saved view identifier.

Query state serializes into the URL. Back/forward, refresh, copy/paste, and
opening in a new tab reproduce the same result set. Changing search/filter/sort
resets cursor history; changing visible columns does not refetch.

### Collection primitives

Build and use these everywhere:

- `AdminDataTable<T>`: virtualized rows, bounded page, stable selection,
  column visibility, density, copyable cells, accessible keyboard navigation.
- `SearchBar`: debounced/cancelable server search with explicit submit for
  expensive queries.
- `FilterBuilder`: metadata-driven fields/operators from the collection
  descriptor; no screen-specific filter grammar.
- `CursorPager`: previous/next and page-size control without fake page counts.
- `SavedViews`: named query/column presets stored per operator.
- `DetailDrawer` and `EntityLink`: consistent inspection and relationship
  navigation without losing list context.
- `JsonViewer`: collapsible, searchable, copyable, size-bounded JSON with raw
  download for large payloads.
- `QueryBoundary`: loading, empty, stale, permission, timeout, and retry states.

No route may implement its own pagination/search stack. A lint/test rule scans
for direct list RPC invocation outside the collection data layer.

## Information architecture

### Overview

- deployment version/schema, API/worker readiness;
- tenants/sessions/runs/resources counts and rates;
- queue depth, retries, failures, oldest job age;
- event append/subscribe lag and active subscriber counts;
- provider health/probe failures and reconcile backlog;
- links to pre-filtered problem views rather than giant dashboard tables.

### Tenants

- tenant list/search/detail;
- API-key metadata/revocation status (never raw keys);
- credentials metadata, providers, sessions, resources, runs, periodic prompts;
- cross-tenant comparison and explicit tenant filter banner.

### Sessions

- searchable sessions with labels and actor summaries;
- unified diagnostic timeline from E7;
- events with seq/type/actor/payload version and detail drawer;
- memory entries, resources, run trees, awaiting questions, labels history;
- replay position and event-gap diagnostics.

### Runs and loop

- runs by status/model/actor/policy/session/time;
- run tree visualization and tabular fallback;
- prompt/messages, policy snapshot, steps, deltas, tool calls/results,
  spawn/wait/cohort state, awaits/answers, terminal failure detail;
- queue attempts correlated per step;
- large prompts/results fetched on demand.

### Resources and providers

- resources by tenant/kind/provider/state/session/age;
- spec/handle/endpoint metadata, epoch/token rotation history, lifecycle events;
- provider registrations, capabilities, probe results, adoption/lister output,
  leaked/unknown native resources, and reconcile attempts;
- links between provider-native identity and core resource state.

### Queue and automation

- River jobs by kind/state/attempt/worker/scheduled time;
- attempt errors and worker ownership;
- periodic prompts, next fire, enqueue history, created runs;
- stuck/retry/dead-letter diagnostic presets.

### Credentials and security diagnostics

- encrypted credential metadata, key version, redaction registration, last use;
- API-key hashes/prefixes/scopes/revocation, never plaintext;
- actor attribution search and policy-denial events;
- operator audit events (fully populated in E7).

### Raw internals

- schema/table inventory from E7;
- raw projected row detail for debugging field/version discrepancies;
- build/config snapshot with secrets redacted;
- download bounded JSON/NDJSON for the active query.

## Work items

### T6.1 — SPA shell and private auth

- Add build/deploy pipeline, private route, login/session expiry, audience
  validation, and no public indexing.
- Admin API origin is separately configured; CSP restricts connect-src to it.
- Include build SHA and API version in the shell.

### T6.2 — Query component library

- Implement query state, descriptor-driven filters, cursor pager, data table,
  saved views, drawers, entity links, JSON viewer, and query boundary.
- Add Storybook or equivalent isolated component harness using fixture data;
  acceptance evidence still runs against the real admin API.
- Prove 250-row pages remain responsive and no route holds prior pages unless
  the operator explicitly pins rows.

### T6.3 — Entity routes

Implement all information-architecture routes above. Each collection route
must declare:

- summary columns;
- searchable/filterable/sortable fields;
- default order and page size;
- detail sections;
- entity relationships and deep links;
- empty/error/permission/large-payload behavior.

### T6.4 — Correlation workflows

Golden workflows:

1. failed run → step → queue attempt → provider error → resource/provider;
2. session event → actor → originating API call/run → related mutations;
3. stuck resource → reconcile history → native lister result → queue job;
4. cross-tenant suspicion → same ID/query across explicit tenant scopes;
5. latency spike → oldest jobs/subscriber lag/provider health.

Each workflow must preserve query/list context when navigating detail.

### T6.5 — Performance and accessibility

- Request cancellation and stale-result suppression on rapid search changes.
- Virtualize within the bounded page; never prefetch every page.
- Lazy-load drawers/tabs and large payloads.
- Define browser memory, JS bundle, request count, and interaction latency
  budgets; gate regressions in CI.
- Keyboard navigation, focus restoration, semantic tables, contrast, and
  screen-reader labels pass automated and manual checks.

### T6.6 — Testing and docs

- Playwright runs the actual SPA against real Postgres + coreadmin.
- Seed large datasets and assert server-side pagination/search via request and
  row-count evidence.
- Add `docs/admin-spa.md`, `phase_e6_inventory.md`, and completion audit.

---

## Acceptance criteria

- **A6.1** Every E5 admin collection/detail/relationship has a reachable SPA
  route and is mapped in `phase_e6_inventory.md`; no placeholder screens.
- **A6.2** SPA network traffic contains only `admin.v1` calls plus static
  assets. Import and runtime route gates fail on any `core.v1` usage.
- **A6.3** Every list uses the shared query primitives. Direct list RPC calls
  outside the collection data layer fail CI.
- **A6.4** URL state reproduces search, filters, sort, cursor/page size, visible
  columns, tenant scope, and selected detail; browser back restores context.
- **A6.5** On 100k+ event and queue datasets, the DOM contains only the bounded
  page/virtual window; interaction remains responsive and browser memory does
  not grow with traversed page count.
- **A6.6** Search requests are debounced/canceled; stale responses cannot
  replace newer results; typed server errors render actionable states.
- **A6.7** The five golden correlation workflows pass in Playwright against
  the real stack, including copyable deep links.
- **A6.8** Secret plaintext never renders, enters browser logs, local storage,
  URL state, error reporting, screenshots, or downloaded ordinary details.
- **A6.9** Accessibility and performance budgets pass; admin SPA deployment is
  private and absent from public navigation/service discovery.

## Test coverage

| Behavior | Evidence | Tier |
|---|---|---|
| Shared query components | component tests + direct-RPC import gate | unit/CI |
| URL/query composition | router tests + Playwright refresh/back/deep-link | browser |
| Full internal surface | inventory verifier + route smoke | CI/browser |
| Correlation workflows | five golden Playwright tests | browser |
| Large-data bounded rendering | seeded real-stack Playwright + memory/DOM assertions | browser/perf |
| Admin API isolation | network allowlist and public-route denial | security |
| Secret non-disclosure | browser storage/log/URL/screenshot scans | security |
| Accessibility | axe + keyboard/manual checklist | browser |

## Exit audit

`phase_e6_audit.md` walks every E5 inventory row to a real screen, executes the
five workflows without database access, verifies no bespoke pagination/search
implementations, and records large-dataset browser performance. E7 may add
mutations only after exploration is complete and safe under realistic scale.
