# Phase 1 — Durable agent loop + minimal web UI

**Duration:** 2–3 weeks · **Depends on:** Phase 0

## Goal

The heart of the system: a crash-safe, horizontally-scalable agent loop built on
fantasy, driven one step per queue job, with all activity observable as typed session
events — plus the first React UI rendering those events live. After this phase, a human
can open a browser, prompt an agent, watch it stream, answer its questions, and reload
mid-run without losing anything.

## Scope

**In:**
- `agent_runs` + `agent_run_steps` tables and store methods.
- Run state machine: `pending → running ⇄ awaiting → completed | failed | cancelled`.
- Step job worker: fantasy agent with `StepCountIs(1)`, history envelope, transactional
  next-step enqueue.
- Loop registry v1 (`loop_kind`, `loop_version` stamped at run creation).
- **BYO inference credentials**: `credentials` table + `/secrets` package (AES-GCM +
  KMS keyring), `OrgService.{PutCredential,ListCredentials,DeleteCredential}`;
  per-step resolution of the run's model config to an org credential → fantasy
  provider (openai, anthropic, bedrock); typed fast-fail when absent/invalid; a
  redaction layer over event payloads, logs, and errors.
- Native tools: `ask_user` (structured question → awaiting), `post_event`.
- `AgentService`: StartRun, PromptRun, CancelRun, GetRun, ListRuns.
- New event variants: `RunStarted`, `StepStarted`, `TextDelta`, `ReasoningDelta`,
  `ToolCallStarted`, `ToolResult`, `StepFinished`, `RunAwaiting`, `RunCompleted`,
  `RunFailed`, `RunCancelled`.
- `testkit/modelscript`: scripted OpenAI-compatible server.
- React SPA v1 (`ui/web`): session list, session view (live event stream, streamed
  text, tool-call cards), prompt box, answer-question UI, org settings page for
  inference credentials (add/rotate/delete; values write-only).
- Playwright suite bootstrapped on the harness.

**Out:** dev envs (Phase 2), multi-run concurrency guarantees beyond correctness
(Phase 3), flows (Phase 4), compaction/fallback (Phase 6).

## Design details

### Inference credential resolution

- `model_config` on a run names `{provider, model_id, credential: "default"|name}`. At
  step start the worker resolves the credential from the org's store, decrypts, and
  constructs the fantasy provider (`providers/openai|anthropic|bedrock`). Resolution
  results are cached per (org, credential, rotation epoch) in the worker.
- No credential of the right kind → the run fails **before** the first step with
  `RunFailed{reason: credential_missing}` — a user-actionable, typed error surfaced in
  the UI with a link to org settings. Auth errors from the vendor (401/403) fail the
  run similarly (`credential_invalid`); they never burn retries.
- The harness seeds each test org with a credential pointing at modelscript — so the
  functional suite exercises the *real* resolution path, not a bypass.
- Redaction: a canary secret embedded in test credentials must never appear in any
  event payload, log line, or error message (asserted in A1.7).

### The step job

```
job step{run_id, step_index}
  1. tx: load run FOR UPDATE; if state ∉ {pending, running} → ack & exit (stale delivery)
  2. load history envelope {"v":1,"messages":[...]}
  3. build fantasy agent from loop registry (loop_kind/version on the run):
       fantasy.NewAgent(model, WithTools(nativeTools), WithSystemPrompt(...),
                        WithStopConditions(fantasy.StepCountIs(1)))
  4. Stream with AgentStreamCall callbacks:
       OnTextDelta / OnReasoningDelta → append TextDelta/ReasoningDelta events (batched,
         flushed on interval/size — see "delta batching" below)
       OnToolCall → ToolCallStarted event
       OnToolResult → ToolResult event
  5. classify outcome:
       Continue  → tx: append messages to envelope, insert step row, append
                   StepFinished, EnqueueTx(step{run_id, step_index+1}), commit
       Await     → tx: persist, state=awaiting, append RunAwaiting(question), commit.
                   NO job parked — the queue is empty while awaiting.
       Complete  → tx: persist, state=completed, append RunCompleted, commit
       Error     → retry per policy; terminal failure → state=failed + RunFailed event
```

- **Idempotent redelivery:** `agent_run_steps` has `UNIQUE(agent_run_id, step_index)`.
  If a worker dies after commit but before ack, the redelivered job sees the step row
  exists and simply re-enqueues `step_index+1` (or observes it already enqueued via a
  dedup key on the job).
- **Crash mid-step:** nothing was committed → redelivery re-executes the step from the
  persisted history. The LLM call may repeat; events from the dead partial attempt are
  bounded by delta batching (deltas are only durable at flush; a `StepStarted(attempt)`
  marker lets clients discard superseded partial deltas).
- **Delta batching:** raw token deltas are coalesced into events every ~100ms or 512
  bytes, whichever first. This bounds event-log write amplification while keeping
  perceived streaming latency low. The event payload carries `(step_index, attempt,
  delta_index)` so ordering and supersession are unambiguous.

### ask_user / awaiting

`ask_user` is a native `fantasy.AgentTool` whose response sets `StopTurn` and returns a
sentinel the outcome classifier maps to `Await`. The structured question (type, text,
choices) travels in the `RunAwaiting` event. `PromptRun(run_id, answer)`:
tx-appends the answer as a user message to the envelope, appends a `UserMessage` event,
sets state `running`, and `EnqueueTx`s the next step. Also used for plain "prompt the
agent again" on a completed run (starts a new turn, same history).

### Cancellation

`CancelRun` sets a `cancel_requested_at` column and appends `RunCancelled` intent. The
step job checks the flag at tx-begin (stale-state guard covers it) and the streaming
callbacks poll a per-run context cancelled via LISTEN/NOTIFY, so an in-flight LLM call
aborts promptly. Terminal-state guard ensures no further steps run.

### modelscript (`testkit/modelscript`)

Standalone binary + Go library: an OpenAI-compatible `/v1/chat/completions` server
(streaming SSE) driven by declarative scripts:

```go
modelscript.Script{
    Turns: []modelscript.Turn{
        {Match: modelscript.UserContains("hello"),
         Respond: modelscript.Text("hi").Then(modelscript.ToolCall("ask_user", q)),
         ChunkDelayMs: 50},
        ...
    },
}
```

Matchers inspect the full message history (so multi-step behavior is scriptable);
unmatched requests fail loudly (500 + test log) rather than defaulting — silent drift is
the enemy. Supports: chunked streaming with configurable pacing, tool calls, forced
HTTP errors (for Phase 6 fallback tests), request capture for assertions.

### React SPA v1

- Stack: vite, react, typescript, tailwind, shadcn/ui, `@connectrpc/connect-web` with
  the generated `clients/ts` package.
- State model: a per-session event reducer — the UI holds `lastSeq` and folds typed
  events into view state (message list, run states, streaming buffers). Reload =
  resubscribe from 0 (later: snapshot + from_seq). This mirrors the testclient and
  keeps UI state provably derivable from the event log.
- Views: session list (create/open), session view (event timeline: user messages,
  streamed assistant text with live caret, tool-call cards with collapsible payloads,
  run status chips), prompt box, `RunAwaiting` question rendered as an inline form.
- Playwright runs against `harness`-launched backend + vite preview build; modelscript
  scripts are seeded per test via a harness endpoint.

## Work breakdown

1. Migrations + store methods for runs/steps; proto additions (AgentService, event
   variants); codegen.
2. `/secrets` package (encryption, keyring, redaction) + credentials store +
   OrgService credential RPCs + per-step resolution.
3. Loop registry + fantasy integration + native tools (`ask_user`, `post_event`).
4. Step job worker with outcome classification + transactional enqueue; `cmd/worker`.
5. Delta batching + attempt markers.
6. Cancellation path.
7. modelscript server + library.
8. Functional tests A1.1–A1.5, A1.7.
9. SPA v1 + event reducer + streaming rendering + org credential settings page.
10. Playwright bootstrap + A1.6.

## Acceptance tests

- **A1.1 — Happy path, exact event sequence.** StartRun with a 3-step script (step 0:
  text; step 1: `post_event` tool call; step 2: final text). Assert: event log contains
  exactly `RunStarted, StepStarted(0), TextDelta+, StepFinished(0), StepStarted(1),
  ToolCallStarted, ToolResult, StepFinished(1), StepStarted(2), TextDelta+,
  StepFinished(2), RunCompleted` in order; run state `completed`; `agent_run_steps` has
  rows 0..2 with nonzero token counts; StartRun's `event_seq` matches the `RunStarted`
  seq.
- **A1.2 — Durability under SIGKILL.** 5-step script, 2s chunk pacing. SIGKILL the
  worker process mid-step-2. Harness starts a fresh worker. Assert: run completes; step
  indices 0..4 unique; event log has a `StepStarted(2, attempt=2)` marker and no seq
  gaps; total steps in DB = 5.
- **A1.3 — Awaiting without parked workers.** Script triggers `ask_user` at step 1.
  Assert: run state `awaiting`; `RunAwaiting` event carries the structured question;
  **queue depth is 0** during the await window (harness inspects river tables);
  `PromptRun` with an answer resumes the run; final history envelope contains the
  answer as a user message; run completes.
- **A1.4 — Cancellation.** Long-paced script; CancelRun mid-stream. Assert: run state
  `cancelled`; `RunCancelled` terminal event present; no `StepStarted` events after it;
  queue drains to 0; a subsequent PromptRun on the cancelled run is rejected with a
  typed error.
- **A1.5 — True streaming.** Subscriber connected before StartRun receives ≥ 2
  `TextDelta` events strictly before `RunCompleted`, with monotonically increasing
  `delta_index`, and inter-arrival gaps proving incremental delivery (not one flush).
- **A1.6 — Playwright golden.** In a real browser: create session → send prompt → see
  text stream in (assert incremental DOM updates) → agent asks a question → answer
  inline → run completes → **reload the page** → full history renders identically from
  the event log → session list shows the session with correct status.
- **A1.7 — BYO credentials.** (a) StartRun in an org with no inference credential →
  `RunFailed{credential_missing}` before any step row exists. (b) Credential present
  but modelscript configured to 401 → `RunFailed{credential_invalid}`, exactly one
  vendor call (no retry burn). (c) Happy path uses the org's credential (modelscript
  asserts the expected API key header). (d) Canary sweep: the planted canary string
  appears nowhere in the event log, ultrad/worker logs, or RPC error messages.
  (e) Org A's credential is unusable from org B (isolation).

## Exit criteria

- A1.1–A1.7 green in CI; functional suite total still < 4 min.
- Two workers running concurrently against one queue pass A1.1–A1.3 unchanged
  (smoke for Phase 3 scale claims).
- `task dev` now boots postgres + ultrad + worker + modelscript + vite dev server.
