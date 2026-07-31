# Phase 6.6 — Complete advanced-loop capabilities

**Goal:** implement the Phase 6 items that the 6.5 audit found incomplete,
with truthful capability-specific Go, dark-mode shadcn, and dark-mode GPUI
evidence.

## Scope

- Switchboard-compatible MCP attachment and `sb/search` / `sb/execute` tools.
- Summarization compaction with persisted summary markers and tail context.
- Sticky ordered fallback chains and per-step serving-model audit.
- Durable cursor hook runner with idempotent aggregate cost accounting and
  auto-title.
- Periodic prompt scheduling, API, native tool, and both client controls.
- OTLP spans for run → step → tool calls with harness collection.

## Exit criteria

Every implemented item has bounded coverage-matrix entries backed by real
Go, shadcn Playwright, and GPUI tests. The Phase 6.5 incomplete list is empty
or any remaining item is explicitly deferred and not claimed.
