# Phase 3 — Multiplayer, multi-loop, agent-spawns-agent

**Duration:** 2 weeks · **Depends on:** Phase 2

## Goal

Make the session genuinely shared: multiple humans, multiple concurrent agent runs, and
multiple envs coexist in one session; agents provision new agents and envs on the fly
with strictly narrowing privilege; a session-scoped memory survives run boundaries. The
horizontal-scaling claims become tested facts.

## Scope

**In:**
- Presence: `SessionService.{Join,Leave,Heartbeat}`, `participants` table,
  `ParticipantJoined/Left/Idle` events, last-seen tracking.
- Concurrency: N runs per session, N envs per session; per-run event attribution;
  per-run history isolation.
- Spawn tools: `spawn_agent`, `wait_for_agents`, `run_agent_cohort` (server-side
  fan-out/fan-in); `parent_run_id` tree.
- Token narrowing: grants (tool subset, env subset) enforced at tool dispatch and
  RPC; children can only narrow.
- `session_memory` table + `session_memory_{get,set,list,delete}` native tools
  (200-key cap, 64KiB/value, advisory locks).
- Event variants: `RunSpawned`, `MemorySet`, `MemoryDeleted`, `PermissionDenied`.
- UI: presence avatars, run lanes / filterable timeline, spawn tree view, memory
  inspector.
- Multi-replica harness mode (2× ultrad, 2× worker).

**Out:** flows (Phase 4), cross-session anything, cross-org anything (participants are
always members of the session's org — enforced since Phase 0), full OIDC + role policy
sweep (Phase 7).

## Design details

### Presence

- `Join` registers a participant (human via client identity, agent implicitly at run
  start) and appends `ParticipantJoined`. `Heartbeat` updates `last_seen` (no event —
  presence *state* is polled/derived, presence *transitions* are events). A reaper job
  marks participants idle after a missed-heartbeat window and appends
  `ParticipantIdle`.
- Clients derive the presence roster by folding participant events + a `GetSession`
  snapshot (roster included) — reconnect-safe.

### Concurrent runs

Already structurally supported (runs are independent job chains); this phase makes it
contractual:

- Every run-scoped event carries `run_id`; the SPA renders either interleaved-with-
  attribution or per-run lanes.
- History isolation: envelopes are per-run rows; a test proves concurrent runs never
  bleed messages.
- Per-session event append remains serialized by the seq counter — measured here for
  throughput (delta batching keeps this comfortable; if it ever isn't, seq allocation
  can batch too).

### Spawning & the privilege lattice

Run tokens carry `grants = {tools: set, envs: set|*, may_spawn: bool, max_children}`.

- `spawn_agent{prompt, model?, grants}`: validates `grants ⊆ parent.grants` (deny with
  `PermissionDenied` event otherwise), mints child token, tx-creates the child run
  (`parent_run_id` set) + `RunSpawned` event + first step job. Returns child run_id
  immediately (async).
- `wait_for_agents{run_ids, timeout}`: **must not hold a worker.** Implemented as an
  await-variant: the tool returns a sentinel → run enters `awaiting` with a
  `waiting_on` record; a completion hook (fired in the same tx that marks any run
  terminal) checks for waiting parents and enqueues their next step with the results
  injected as the tool result. Timeout via a scheduled job.
- `run_agent_cohort{specs[], timeout}`: server-side composition of N spawns + wait —
  one tool call, fan-out/fan-in handled by the store, results collected into a single
  ToolResponse (agents-work lesson: keep reliability mechanics out of prompts).
- Env access: a child granted `envs: [env-A]` gets only `env:A/*` tools; calling
  `terminate_env` on an ungranted env → typed permission error (tool-level, plus a
  `PermissionDenied` session event for visibility).
- No credential inheritance: child envs mint their own tokens; integration credentials
  (Phase 6, switchboard) are attached per-env, never copied from parent.

### Session memory

- `session_memory(session_id, key, value jsonb, updated_by, updated_at)`; writes take
  `pg_advisory_xact_lock(hash(session_id))`; cap checks inside the lock (201st key
  rejected, >64KiB value rejected) — the agents-work flow-memory design, session-scoped.
- Dotted-namespace key convention documented (`investigation.findings.db`), enforced
  only by convention + linting in flow definitions later.
- Every write appends `MemorySet{key, updated_by}` (value elided from the event if
  >1KiB; inspector fetches via RPC).

### Multi-replica harness

`harness.Up(t, harness.WithReplicas(2, 2))`: 2 ultrad behind a tiny round-robin proxy,
2 workers on one queue. Used by A3.1/A3.6 and reused in Phase 7 chaos tests.

## Work breakdown

1. Proto additions (presence RPCs, new events, memory RPC for inspector) + codegen.
2. participants table + join/leave/heartbeat + reaper job.
3. Grants model + token minting/validation + dispatch-time enforcement.
4. spawn_agent + RunSpawned + tree persistence.
5. wait_for_agents await-variant + completion hook + timeout job.
6. run_agent_cohort composition.
7. session_memory store + tools + caps + advisory locks.
8. Multi-replica harness mode.
9. UI: presence, lanes/filter, spawn tree, memory inspector.
10. Tests A3.1–A3.7.

## Acceptance tests

- **A3.1 — Identical ordered delivery.** Two testclients subscribe (one per ultrad
  replica) while a run streams. Both receive byte-identical event sequences in
  identical order. Presence: client A joins → B sees `ParticipantJoined` live; A's
  `Append(UserMessage)` is visible to B no later than A receives its own ack seq.
- **A3.2 — Concurrent run isolation.** Two runs with distinct scripts and distinct envs
  execute concurrently in one session. Assert: events interleave but every run-scoped
  event carries the correct `run_id`; final histories contain only their own messages;
  both envs did only their own work (harness exec check in each container).
- **A3.3 — Cohort fan-out/fan-in.** Parent script calls `run_agent_cohort` with 2 child
  specs (each provisioning its own env and writing a distinct file). Assert: 2 child
  runs with `parent_run_id` set; parent is `awaiting` with **queue depth 0** while
  children work; parent resumes after both complete; the cohort ToolResult contains
  both children's results; spawn tree queryable via GetRun/ListRuns.
- **A3.4 — Privilege narrowing.** Child spawned with `tools: [bash, view]`, `envs:
  [env-B]`. Assert: child calling `terminate_env(env-A)` → typed permission error in
  ToolResult + `PermissionDenied` event; child calling `spawn_agent` with
  `tools: [bash, view, terminate_env]` (broader) → denied; a compliant narrower
  grandchild spawn succeeds.
- **A3.5 — Memory safety and caps.** 10 concurrent writers to the same key: final value
  is one of the written values, `MemorySet` event count == successful writes (no lost
  updates). 201st key → typed error. 65KiB value → typed error. Run 2 reads what run 1
  wrote after run 1 completed.
- **A3.6 — Horizontal scale.** With 2 ultrad + 2 workers: A1.1–A1.3 pass unchanged;
  runs started via replica A stream to subscribers on replica B; SIGKILL one worker
  mid-run under concurrent load (4 runs) — surviving worker completes all runs, no
  duplicate step indices anywhere.
- **A3.7 — Playwright golden.** Two browser contexts in one session: both see live
  presence avatars; a cohort flow renders as a spawn tree; filtering the timeline by
  run works; the memory inspector shows a value written by an agent tool call.

## Exit criteria

- A3.1–A3.7 green in CI (multi-replica leg included).
- Grants documented (`docs/security.md`): lattice rules, token format, enforcement
  points.
- Event append throughput under A3.2 load measured and recorded as a baseline.
