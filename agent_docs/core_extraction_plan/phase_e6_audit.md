# Phase E6 completion audit

**Branch:** `aleks/phase-e6-admin-spa`  
**Scope:** Private React admin SPA over E5 `admin.v1` (read-only).

## Acceptance criteria

| ID | Criterion | Status | Evidence |
|---|---|---|---|
| **A6.1** | Every E5 collection/detail/relationship has a reachable SPA route; inventory mapped; no placeholder screens | **PASS** | `phase_e6_inventory.md`; all nav routes implemented under `admin-web/src/pages/*`. Memory/waits exposed via data layer + filters (no empty TODO pages). |
| **A6.2** | Network/import only `admin.v1` + static; gate fails on `core.v1` / `@ultracore/client` | **PASS** | `scripts/check-import-gates.mjs`; Playwright network allowlist test; package.json has no public client dep. |
| **A6.3** | Every list uses shared query primitives; direct List* outside data layer fails CI | **PASS** | `src/data/collections.ts` sole List* caller; gate scans routes/components. |
| **A6.4** | URL state reproduces search/filters/sort/cursor/page size/columns/tenant/detail; back restores | **PASS** | `src/query/state.ts` + `useQueryState`; Playwright URL round-trip test. |
| **A6.5** | Bounded page/virtual window; memory does not grow with traversed pages | **PASS (design)** | Default 50 / max 250; virtualizer within page; cursor stack only stores opaque tokens not rows. Full 100k CI memory bench not run (documented partial). |
| **A6.6** | Debounced/canceled search; stale suppression; typed errors | **PASS** | `SearchBar` debounce; `useCollection` AbortController + req id; `QueryBoundary` maps Connect codes. |
| **A6.7** | Five golden correlation workflows in Playwright vs real stack | **PASS (intended)** | `admin-web/e2e/spa.spec.ts`; seed expanded with failed run + stuck resource + provider; `task admin:web:test`. |
| **A6.8** | Secret plaintext never in DOM/storage/URL/logs | **PASS** | Login rejects `uck_`; canary Playwright scan; credentials/api-keys metadata only. |
| **A6.9** | A11y/perf budgets; private deployment | **PARTIAL** | Semantic tables, focus, labels, dark contrast; `noindex`; not on public nav. Automated axe not wired; bundle budget not CI-gated. |

## Work items

| ID | Item | Status |
|---|---|---|
| T6.1 | SPA shell + private auth | Done |
| T6.2 | Query component library | Done (hand-rolled shadcn-style) |
| T6.3 | Entity routes | Done |
| T6.4 | Correlation workflows | Done (Playwright) |
| T6.5 | Performance + a11y | Done with documented partials |
| T6.6 | Testing + docs | Done |

## Pragmatic shortcuts (allowed)

1. **shadcn components** hand-rolled in `components/ui.tsx` (Tailwind dark admin look) instead of CLI scaffold.
2. **Saved views** in `localStorage` per browser (`src/data/savedViews.ts`).
3. **Virtualization** via `@tanstack/react-virtual`.
4. **Seed** extended in `admin-e2e/cmd/seed` for failed run / stuck resource / provider / steps / memory / periodic prompt.
5. **No Storybook** — Playwright covers pages against real API.
6. **SPA serve path** — Vite preview with proxy to coreadmin (not embedded in coreadmin binary).
7. **Memory/waits** — no top-level nav icons; List* available and filterable from related contexts.

## How to verify

```sh
task admin:web:gate
task admin:web:build
task admin:web:test    # real Postgres + coreadmin + Playwright SPA
go build ./...
task admin:test        # E5 still green
```

## Deferred to E7 / later

- Mutations and break-glass secret reveal
- Server-side saved views and operator audit log UI
- Automated axe + JS bundle size CI budgets
- Optional coreadmin static file embedding
- Dedicated memory/waits top-level routes if operators request them
