# Multiplayer and session memory

Phase 3 adds durable human/agent presence, concurrent run trees, a
monotonically-decreasing grants model, and capped session-scoped JSON memory.

Presence lives in `participants`; Join/Leave append typed transition events,
Heartbeat updates last-seen without event noise, and snapshots come from
ListParticipants. Agent runs implicitly join when created.

`Grants` restrict canonical tools, environment IDs, spawning, and child
counts. Root human-started runs receive server-defined root grants; children
may only narrow authority. Persisted grants are authoritative.

`session_memory` is protected by a per-session Postgres advisory transaction
lock. It allows 200 keys and 64KiB per value. Human APIs and agent native
tools share the same store implementation, so memory survives run and worker
boundaries.

`parent_run_id`, `run_waits`, and `run_wait_members` provide the durable
schema for child trees and fan-in waits. Terminal-child resolution is
transactional and ordered by member ordinal.
