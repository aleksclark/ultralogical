# Phase 6 — Advanced loop & tool ergonomics

**Duration:** 2 weeks · **Depends on:** Phase 4 (Phase 5 useful but not required)

## Goal

Apply the switchboard and crush-modules learnings to make the loop production-smart:
integration tools via switchboard sidecars, context management (compaction), model
fallback chains, background hooks, periodic prompts, and full OTLP tracing. Everything
lands as observable events — no invisible magic in the loop.

## Scope

**In:**
- Switchboard attachment: optional per-env sidecar exposing `search`/`execute`
  meta-tools to runs; per-env integration credentials (no inheritance).
- Context management via fantasy `PrepareStep`: size-capped history compaction with a
  summarization sub-call; `HistoryCompacted` event; history envelope `v2` (compaction
  markers).
- Model fallback chains: per-agent `fallbacks: []` in flow/model config;
  `ModelFallback` event; retry/fallback policy at the step level.
- Hooks v1: background processors subscribed to the session event stream, registered
  server-side (not user-supplied code yet): `auto-title` (names sessions from early
  events), `cost-accounting` (aggregates step token usage into session memory under
  `system.cost.*`).
- Periodic prompts: per-session cron entries that append a prompt to a designated run
  (or start one) on schedule; queue-scheduled, toggleable via RPC + native tool.
- OTLP: spans `session ⇒ run ⇒ step ⇒ tool_call` with usage/cost attributes; trace
  context propagated into bezalel/switchboard HTTP calls.
- Event variants: `HistoryCompacted`, `ModelFallback`, `HookFired`,
  `PeriodicPromptFired`.
- UI: compaction/fallback badges in the timeline, session cost display, periodic
  prompt management panel.

**Out:** user-supplied hook code (sandboxing is post-v1), switchboard marketplace
integration, prompt-caching optimizations (tracked, not gated).

## Design details

### Switchboard attachment

- `EnvSpec.integrations: {switchboard: {config_ref, integrations: [...]}}` → the
  provider adds a switchboard sidecar (container next to bezalel; same pod on k8s, task
  group on nomad, extra container on local) with credentials resolved from the **org's
  credential store** (`integration:*` kinds, BYO like inference creds) — scoped to this
  env, minted per attachment, never platform-shared across orgs.
- The mcpTool adapter discovers switchboard's meta-tools exactly like bezalel's; they
  appear as `sb/search`, `sb/execute` (namespaced per env if multiple). The meta-tool
  pattern means the run sees 2 tools, not 1100.
- Grants: `sb/*` is a grant like any other; children don't inherit credentials —
  spawning a child that needs integrations requires an env (or attachment) of its own.

### Compaction (PrepareStep)

- Trigger: serialized history size > threshold (per-model, default ~70% of context
  window) at step start.
- Mechanism: `PrepareStep` returns a mutated message list: system prompt + a summary
  message (produced by a bounded summarization call to the same/cheaper model) +
  the tail N messages. The **full** history remains in the envelope (v2 adds
  `compactions: [{at_step, summary_message_idx, covered_range}]`); compaction changes
  what the model sees, never what the log stores. Deterministic re-derivation on
  redelivery: the summary is persisted in the envelope inside the same step tx.
- `HistoryCompacted{covered_range, summary_tokens}` event for visibility.

### Fallback chains

- Step execution wraps the model call: on retryable provider failure (5xx, timeout,
  rate-limit after budget), advance to the next model in the chain for **this and
  subsequent steps** (sticky until run end), emit `ModelFallback{from, to, reason}`.
  Non-retryable (4xx auth/validation) → run `failed` fast.
- The step row records which model actually served it.

### Hooks v1

```go
type Hook interface {
    Name() string
    Match(e Event) bool
    Handle(ctx context.Context, s Store, e Event) error // must be idempotent
}
```

- Driven by a queue job per (hook, session) batch with a durable cursor
  (`hook_cursors(session_id, hook, last_seq)`) — hooks are at-least-once, resume from
  cursor, never block the append path.
- `auto-title`: after the first user message + first completion, sets `sessions.title`
  via a small LLM call (modelscript-able), appends `HookFired`.
- `cost-accounting`: folds `StepFinished` usage into `session_memory["system.cost"]`.

### Periodic prompts

- `periodic_prompts(session_id, schedule cron, prompt, target)` + a scheduler job
  (queue-native periodic job) that enqueues prompt-append at fire time; if the target
  run is mid-step, the prompt lands as the next user message (queued, not dropped);
  `PeriodicPromptFired` event either way. Toggleable via RPC and via a native tool
  (the crush-modules pattern: the agent can manage its own cadence).

### OTLP

- `cmd/ultrad` and `cmd/worker` ship with OTLP exporters (env-configured). Span tree:
  run span (linked to session via attribute), step spans, tool_call child spans with
  tool name/env/duration/error; LLM call spans carry token usage. `traceparent` headers
  flow into bezalel and switchboard so their spans join the trace.

## Work breakdown

1. Switchboard sidecar support in local + k8s + nomad providers; credential minting.
2. mcpTool discovery of switchboard meta-tools + grants.
3. Envelope v2 + compaction in PrepareStep + summarization call + event.
4. Fallback chain wrapper + step-row model attribution + event.
5. Hook runner (cursors, batching, idempotency) + auto-title + cost-accounting.
6. Periodic prompts scheduler + RPC + native tool.
7. OTLP instrumentation + harness collector.
8. UI: badges, cost display, periodic prompt panel.
9. Tests A6.1–A6.5.

## Acceptance tests

- **A6.1 — Switchboard meta-tools, real binary.** Env provisioned with a switchboard
  sidecar configured against a scripted upstream (a fake GitHub API served by the
  harness — real switchboard binary, fake vendor). Scripted run: `sb/search "list
  issues"` → finds the tool → `sb/execute github_list_issues` → compacted results in
  the ToolResult event. Assert: the switchboard container is real (harness inspects),
  credentials never appear in events or run history, a child spawned without the grant
  cannot call `sb/*`.
- **A6.2 — Compaction preserves behavior and history.** Script a run whose history
  exceeds a deliberately tiny cap at step 3. Assert: `HistoryCompacted` event with
  correct covered range; the *stored* envelope still contains every original message
  (full-fidelity log); the step-3 LLM request (captured by modelscript) contains the
  summary + tail, not the full history; the run completes with the scripted final
  answer; SIGKILL between compaction and step completion → redelivery uses the
  persisted summary (no second summarization call — modelscript call count proves it).
- **A6.3 — Fallback.** Modelscript primary endpoint returns 500s after step 1;
  secondary scripted normally. Assert: `ModelFallback` event with reason; steps ≥ 2
  recorded against the secondary model; run completes; a 401 from primary instead
  fails the run fast with `RunFailed` (no futile fallback on auth errors).
- **A6.4 — Hooks are durable and idempotent.** cost-accounting: after a 3-step run,
  `session_memory["system.cost"]` equals the sum of step usages. Kill the worker while
  the hook batch is mid-flight → cursor resume produces the same final value (no
  double-count). auto-title: session title set within one hook interval, `HookFired`
  event present.
- **A6.5 — Traces join up.** Harness OTLP collector receives spans forming
  `run → step → tool_call` with correct parentage and session/run attributes; a bezalel
  tool call's server-side span (bezalel is traced or the client span carries the env
  attribution) links to the step trace; token usage attributes match step rows.

## Exit criteria

- A6.1–A6.5 green in CI.
- Compaction + fallback documented in `docs/loop.md` (behavior, events, envelope v2).
- Golden live suite (nightly, real LLM) extended to cover one compaction and one
  ask_user scenario — guarding modelscript against drift from real providers.
