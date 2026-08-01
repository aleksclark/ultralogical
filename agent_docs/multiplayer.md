# Multiplayer, orchestration, and session memory

Durable human/agent presence, concurrent agent trees, a monotonically
decreasing grants model, and capped session-scoped JSON memory. Phase 3
introduced the schema; Phase 8 completed the state machines and the races.

## Grants

`Grants` restrict canonical tools, environment IDs, spawning, and child
counts. Root human-started runs receive server-defined root grants; children
may only narrow authority. Persisted grants are authoritative, and every
capability is subject to them — including the built-in `ask_user`,
`post_event`, and session-memory tools.

Authority is decided **at dispatch**, not at discovery. Discovery filtering
only chooses what the model is offered; a call can still arrive replayed from
a step whose grants were wider, or simply invented. Every native tool, spawn,
wait, and environment MCP call rechecks.

A run created with an empty grant record may do nothing. There is no fallback
that treats "no grants" as "unspecified, so root".

Denials are uniform: every refusal returns `permission denied` with no
resource name and no distinction between forbidden and nonexistent. Because
the agent framework answers an unregistered tool call by listing every tool
that does exist, ungranted capabilities are registered as explicit denial
stubs (`StepWorker.denialStubs`) rather than omitted. Each denial appends a
`permission_denied` event, so the audit channel is the event log while the
model's view stays opaque.

See [docs/security.md](../docs/security.md) for the full model and
`e2e/phase8_security_test.go` for the executable proof of each claim.

## Presence

Presence lives in `participants`. Join/Leave append typed transition events,
Heartbeat updates last-seen without event noise, and snapshots come from
ListParticipants. Agent runs implicitly join when created.

Stale presence expires durably. `loop.PresenceReaper` runs as a
self-rescheduling job (`PresenceReapJob`), transitioning participants whose
last heartbeat is older than `ULTRA_PRESENCE_AFTER` (default 45s) to idle and
appending the `participant_idle` event in the same transaction. Joining arms
the reaper, so a session with participants always has expiry scheduled.

## Session memory

`session_memory` is protected by a per-session Postgres advisory transaction
lock. It allows 200 keys and 64KiB per value, with key names validated by
`ultra.ValidMemoryKey`. Human APIs and agent native tools share the same store
implementation, so memory survives run and worker boundaries.

Agent writes append `memory_set` / `memory_deleted` events in the same
transaction as the write. Memory is shared with humans and other agents, so a
silent write would leave every subscriber with a stale view until they
happened to re-read. Values above 1KiB are not inlined into the event; the
event names the key and subscribers fetch the value through the API, so the
log does not become a second copy of memory.

## Child spawning

`spawn_agent` creates one child with strictly narrower authority.

**Idempotency.** Each child carries a `spawn_key` of
`"{parent}:{step}:{toolCallID}"`, unique per org. A redelivered step therefore
adopts the child it already created instead of making a second one. The
spawn transaction locks the parent row (`GetForUpdate`) and re-checks the key
inside the transaction; the parent lock is also what makes `max_children`
correct when several spawns run concurrently within one step.

**Atomic first step.** Child creation, its `run_spawned` event, and the
enqueue of its first step job all commit together. A child row can never exist
without work scheduled for it.

## Waits

`wait_for_agents` parks a parent until named children are terminal. The parent
leaves the queue entirely — it holds no worker — and is resumed by whichever
path closes the wait. `run_ids: ["*"]` waits on every child the run has
spawned so far, so a model need not echo generated identifiers. A parent may
only wait on runs it actually parented.

Waits are a transactional state machine over `open | resolved | timed_out |
abandoned`. Exactly-once resolution and at-most-once resumption are enforced
by database predicates, never by process state:

| Guarantee | Mechanism |
|---|---|
| A wait resolves once | `Close()` carries `WHERE state='open'`; a second caller affects zero rows |
| A parent resumes once | `MarkResumed()` carries `WHERE resumed_at IS NULL` |
| A wait times out once | `ClaimDue()` performs the `open → timed_out` transition *as* the claim |
| A parent holds one open wait | partial unique index `run_waits_one_open_per_parent_idx` |

**Pre-commit race.** `createWait` creates the wait row and evaluates its
members inside one transaction, returning whether the parent should park. If
every member was already terminal, the wait resolves and the parent resumes
without ever being marked awaiting.

**Timeouts.** The timeout job is enqueued in the same transaction as wait
creation, slightly past the deadline, so a wait can never exist without
something scheduled to expire it. `loop.WaitSweeper` claims due waits in
batches and re-arms itself on truncation or error. The default timeout is five
minutes; `timeout_policy` chooses whether expiry resolves the parent with
partial results or fails it.

**Result correlation.** On resolution the parent's history receives exactly
one tool message whose `ToolCallID` is the original `wait_for_agents` call,
carrying each member's terminal state and persisted result. A child's result
is durable (`RunStore.SetResult`), so a parent can read what its child
produced long after the child's process is gone.

**Terminal paths.** Child completion, child failure, child cancellation,
parent cancellation, timeout, and worker death all route through the same
durable resolution policy. Cancelling a parent abandons its open waits
(`loop.AbandonWaits`).

## Cohorts

`run_agent_cohort` is server-side fan-out/fan-in built on the same child and
wait primitives — not a prompt convention and not model-side polling. It
creates up to `maxCohortSize` (16) children in declaration order, stamping
each with a shared `cohort_id` and its `cohort_ordinal`, then opens a single
cohort wait over all of them. The aggregate result preserves declaration
order and exposes each member's terminal state and result, so one failed
member is visible as a failure rather than a missing entry.

## Run trees

`GetRunTree` returns the nested spawn tree with each run's waits, which is what
clients render as a run tree and use to filter the timeline to one agent's
lane. Web evidence is `ui/web/e2e/run-tree.spec.ts`; GPUI evidence is
`ui/desktop/tests/run_tree_e2e.rs`.
