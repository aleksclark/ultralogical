# Phase 6.5 capability audit

The independent audit found Phase 6 only partially implemented. The following
remain explicitly **incomplete** and are not claimed in `e2e/coverage.json`:

- Switchboard sidecars and `sb/search` / `sb/execute` tools.
- Summarization-based compaction and crash-idempotent summary reuse.
- Sticky multi-model fallback chains and per-step serving-model audit.
- Durable cursor-based hook runner, auto-title, aggregate/idempotent cost hook.
- Periodic prompt scheduler/RPC/native tool/UI.
- OTLP trace export and cross-service propagation.
- Compaction/fallback/cost badges and management controls in shadcn + GPUI.

Implemented and retained from Phase 6: additive event/schema support,
envelope-v2 compaction markers while preserving full history, first
retryable fallback attempt, and inline durable `system.cost.latest`
accounting. These are treated as internal groundwork until capability-specific
Go, shadcn Playwright, and GPUI evidence exists.

The audit also removed the false `advanced_loop_cost_hook` coverage row.
Future work must add bounded rows only after tests assert the actual event,
persisted state, failure semantics, replay, and tenancy behavior.
