# Phase 11 — Advanced loop, integrations, automation, and tracing

**Duration:** 3 weeks · **Depends on:** Phase 10

## Goal

Close every audited Phase 6 gap. Integration discovery/execution, context compaction, fallback, hooks, titles, periodic prompts, and traces must be durable product behavior rather than configuration records or marker events.

## Scope

**In:**

- A pinned real Switchboard sidecar and preserved search/execute/session/pin/history semantics.
- Model-generated summarization compaction with durable recovery and bounded context.
- Sticky model fallback with per-step model/attempt audit and restart-safe selection.
- Durable cursor-based hooks, an auto-title hook, retry/dead-letter behavior, and loop prevention.
- A real periodic scheduler and native management tool with exactly-once firing semantics.
- OTLP traces for sessions, runs, steps, model attempts, tool calls, waits, environments, and hooks with redaction and correlation.
- Dark shadcn and rendered GPUI surfaces for integrations, compaction, fallback, hooks, schedules, and traces.
- A scheduled live-LLM contract suite.

**Out:** OIDC, billing, retention, queue swap, production chaos, and deployment hardening (Phase 12).

## Required implementation sequence

1. Inventory every Phase 6 promise and distinguish records/configuration from behavior. Name tests that prove actual execution, restart recovery, failure, and application visibility.
2. Pin Switchboard source/image and launch it in the harness. Preserve its meta-tool protocol rather than exposing the raw catalog. Scope credentials and working context by org/run and redact them everywhere.
3. Implement compaction through a configured summarizer model. Persist summary content, source range, model, token counts, and version; retain recent messages and tool-call/result validity; make redelivery idempotent.
4. Implement ordered fallback attempts with typed retryability. Once a fallback succeeds, keep that model sticky for subsequent steps until a documented reset; persist every attempt and selected model before enqueuing the next step.
5. Build hooks as durable event consumers with per-hook/session cursors, transactional effects, idempotency keys, retries, backoff, dead letters, enable/disable, and self-trigger prevention.
6. Implement auto-title as a real hook over session events. Respect explicit human titles and prove replay/retry does not overwrite or duplicate.
7. Build a scheduler that claims due prompts safely across workers, records each occurrence identity, appends or starts the configured action exactly once, computes the next occurrence, catches up according to policy, and exposes a native management tool.
8. Add OTLP spans with stable parentage and IDs that correlate to persisted entities/events. Export to a real collector in the harness and assert attributes, status, timing boundaries, and secret redaction.
9. Build user-facing integration search/execute, pinned results/history, compaction state, fallback attempts, hook health/dead letters, periodic schedules/firings, and trace links in both applications.
10. Run a small scheduled suite against a real supported LLM to validate modelscript streaming, tool calls, usage, summarization, and fallback assumptions. Keep credentials out of forks and artifacts.
11. Independently audit behavior after worker/ultrad restart and duplicate queue delivery. CRUD-only tests cannot close execution bullets.

## Acceptance tests

- **A11.1 — Switchboard semantics.** Through a real sidecar, search compacts a catalog, execute invokes an allowed integration, session context reduces repeated arguments, pin/history survive context compaction as documented, denied tools remain undiscoverable and fail forged dispatch, and credentials never appear in logs/events/history/traces.
- **A11.2 — Summarization compaction.** Exceed the context threshold with mixed text and tool calls. A real summarizer produces a persisted summary tied to an exact source range; recent context remains; the next answer depends on summarized facts; retries/restarts do not duplicate summaries; total context is bounded.
- **A11.3 — Sticky fallback.** The primary emits retryable failures, fallback succeeds, and later steps start with that fallback. Nonretryable failure stops immediately. Restart between steps preserves selection. Step audit and UI show ordered attempts, model, reason, latency, and usage.
- **A11.4 — Durable hooks and title.** Two workers race hook processing and produce one effect per event. Kill a worker after effect commit but before cursor advancement and prove idempotent recovery. Poison events retry then dead-letter. Auto-title sets one generated title, never loops, and never overwrites a human title.
- **A11.5 — Periodic firing.** Two workers claim the same due occurrence and produce one firing. Disable prevents future firing; restart catches up or skips according to policy; timezone/DST behavior matches the documented schedule; native tool, API, web, and GPUI show identical next/last occurrence and result.
- **A11.6 — OTLP.** A real collector receives one correlated trace across API mutation, queue delay, step, model attempts, tool call, wait, environment call, hook, and event append. IDs match persisted entities, errors are marked, and planted secrets are absent from attributes/events/exported payloads.
- **A11.7 — Application control.** Web and GPUI configure and exercise an integration, inspect pinned/history state, force compaction and fallback, enable/disable a hook, create and observe a periodic firing, inspect failures/dead letters, follow a trace, reconnect, and recover identical state.
- **A11.8 — Live LLM contract.** Scheduled tests verify streamed deltas, tool-call/result correlation, token usage, summarization, and one fallback path against supported real providers; modelscript fixtures are updated only after reviewed contract differences.
- **A11.9 — Restart and duplication matrix.** For Switchboard calls, compaction, fallback, hooks, and scheduler, inject worker death and duplicate delivery at each transaction boundary and prove durable idempotency and eventual terminal state.

## Validation commands

```sh
task generate
task verify:codegen
task lint
go test ./loop/... ./e2e/... -count=1 -timeout 20m
task test:functional
task web:test
cargo test --manifest-path ui/desktop/Cargo.toml
python3 scripts/verify-coverage.py
git diff --check
```

CI must additionally boot pinned Switchboard and an OTLP collector. The live-LLM suite runs on its documented protected schedule.

## Exit criteria

- A11.1–A11.9 pass in required CI or the explicitly scheduled live-provider job.
- Compaction contains a real persisted summary; fallback is sticky and audited; hooks consume durable cursors; schedules fire; traces are exported and correlated.
- Every advanced behavior survives restart and duplicate delivery without relying on one process's memory.
- Every Phase 6 audit bullet is closed by bounded real-stack, shadcn, and rendered GPUI evidence.
