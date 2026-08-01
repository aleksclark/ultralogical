# Durable agent loop

Implemented in `loop/` and `cmd/worker` (Phase 1). Full rationale:
`plan/phase_1.md`.

## One job = one step

`loop.StepJob{run, org, session, step_index}` loads the persisted fantasy
message envelope, resolves the org's inference credential, builds a fantasy
agent with `StepCountIs(1)`, streams the model call + tool executions into
typed session events, then commits one of:

- **continue** — history + step audit + StepFinished + next StepJob, one tx;
- **await** — history + step audit + `state=awaiting` + RunAwaiting, no job;
- **complete** — history + step audit + `state=completed` + RunCompleted;
- **fail/cancel** — terminal state + typed terminal event.

`agent_run_steps PRIMARY KEY(run, step_index)` guards redelivery. If a
worker dies before commit, River rescues and reruns from persisted history;
StepStarted attempt markers tell clients which partial delta stream wins.
If it dies after commit but before ack, the step row makes redelivery stale.

## History

`loop.Envelope{"v":1,"messages":[]fantasy.Message}` is stored on each run.
Only committed steps enter history. `PromptRun` appends a user message and
enqueues the next step. Envelope versioning is part of loop versioning;
never change v1 semantics in place.

## Streaming

Fantasy callbacks become typed events: StepStarted, TextDelta,
ReasoningDelta, ToolCallStarted, ToolResult, StepFinished, and terminal run
events. Deltas batch at 100ms / 512 bytes; tool-call callbacks flush pending
text first. `(step_index, attempt, delta_index)` defines order and crash
supersession.

## Native tools

- `ask_user` returns `StopTurn`, records a structured question, and parks the
  run awaiting input. No worker or queue job is held.
- `post_event` appends an agent-authored Annotation to the session log.

## Environment tools when an environment dies

Environment tools are discovered per step through the epoch-keyed MCP client
cache. An environment can die between the state write that marked it ready and
the next step's discovery. Dropping its tools in that window would silently
shrink the model's capabilities mid-run and hide the failure, so the cache
remembers the last discovered tool names and the resolver re-offers them as
stubs that fail with a typed `environment unavailable` result. A call against a
lost environment is therefore always observable as an error-flagged
`ToolResult`, never as a missing tool or a hang. Calls that do reach a wedged
environment are bounded by `toolCallTimeout` instead.

## Credentials

`ModelConfig{provider, model_id, credential}` resolves inside the worker via
`OrgScope.Credentials()`. Supported fantasy providers: OpenAI, Anthropic,
Bedrock. Credential payloads contain `api_key`, optional `base_url`, and
optional `extra_headers` (string map for gateways such as Cloudflare AI
Gateway). All fields are AES-GCM encrypted in Postgres; secret/header values
are registered with `secrets.DefaultRedactor`. Missing/invalid
credentials fail fast with typed reasons; raw provider errors never reach
users.

## Testing

`testkit/modelscript` is a real OpenAI-compatible HTTP/SSE server with
scripted text/tool calls, chunk pacing, status failures, request capture,
and strict unmatched-request errors. The harness starts real ultrad + worker
processes against real Postgres; only the external LLM vendor is replaced.
