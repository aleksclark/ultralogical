# Flows

A flow is an org-scoped, immutable, versioned declaration: typed parameters,
declared environments, and an agent topology. See [docs/flows.md](../docs/flows.md)
for the full definition-language reference, lifecycle, CLI, and examples.

## Where the code lives

| Concern | Location |
|---|---|
| Definition language, validation, deterministic rendering | `flowdef.go` (root package, no I/O) |
| Domain types and the `FlowStore` seam | `flow.go` |
| Durable invocation state machine | `flowwork/` |
| Persistence | `postgres/flow.go`, migration `00008_flow_completion.sql` |
| Transport | `http/flow_handler.go`, `proto/ultra/v1/flow.proto` |
| Catalog spawning (`agent_ref`) | `loop/spawn.go` |
| CLI | `cmd/ultra/`, `cmd/ultra/cli/` |
| Clients | `ui/web/src/components/flow-panel.tsx`, `ui/desktop/src/window.rs` |

## Invariants worth knowing before you change anything

- **Versions are immutable.** `flows.definition` is `text`, not `jsonb`, so a
  stored version reads back byte-for-byte. Auto-assignment holds a per-`(org,
  name)` advisory lock, so concurrent writers converge on distinct ascending
  versions rather than racing `max(version)`.
- **The rendering is frozen at invoke time.** `flow_invocations.rendered` holds
  the resolved prompts, grants, and environment specs. Nothing re-renders from
  the definition afterwards, which is why a later version cannot alter an
  in-flight or replayed invocation.
- **Rendering is deterministic.** Every traversal is over sorted keys. A change
  that iterates a map directly breaks replay reproducibility.
- **Orchestration is durable state, not handler code.** `flowwork.Service.Advance`
  performs one committed stage transition per delivery and re-arms itself.
  `ClaimAdvance` gives exactly one worker the tick, so redelivery cannot
  multiply polling chains.
- **Progress is keyed.** Every lifecycle step is recorded under an idempotency
  key and mirrored into the session log, so a redelivered advance records
  nothing twice and replay reconstructs the same ordered history.
- **Ownership scopes cleanup.** Runs and environments carry
  `flow_invocation_id` plus their declaration name from the moment they are
  created. Cleanup terminates exactly those rows; a session's other resources
  are never touched, and unique indexes on `(invocation, declaration)` make
  retries adopt rather than duplicate.
- **Declaration is the whole contract.** A flow agent never receives `env_all`;
  it gets exactly the environments it declared. `agent_ref` resolves the flow's
  own prompt, tools, and environments server-side, and a non-spawnable agent is
  refused exactly like one that does not exist.
