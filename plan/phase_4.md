# Phase 4 — Flows

**Duration:** 1–2 weeks · **Depends on:** Phase 3

## Goal

Pre-configured, versioned sets of agents + envs that can be invoked into any session
with parameters. A flow captures the topology (which agents, which envs, who spawns
whom) so invoking "code-review" or "incident-triage" is one call, with full provenance
from every run back to the flow version that produced it.

## Scope

**In:**
- `flows` + `flow_invocations` tables (org-scoped: flows are per-org catalog entries;
  invocations resolve names within the caller's org); flow definitions as validated,
  versioned JSONB.
- `FlowService`: PutFlow, GetFlow, ListFlows, InvokeFlow.
- Definition rendering: Go `text/template` prompts with typed params.
- Version pinning: invocation stamps `flow_id(name,version)` + `loop_version` on all
  runs it creates; in-flight invocations are immune to new flow versions.
- Event variants: `FlowInvoked`, plus `flow_invocation_id` attribution on spawned
  runs/envs.
- CLI: `ultra flow put -f flow.json`, `ultra flow invoke <name> --param k=v
  [--session <id>|--new-session]`, `ultra flow list`.
- UI: flow catalog, invoke form (schema-derived params), invocation provenance on run
  cards.

**Out:** external triggers (Slack/GitHub/webhook/cron) — post-v1; flows are invoked via
API/CLI/UI only. Sub-agent markdown definitions (Phase 6 folds them into flow configs).

## Design details

### Flow definition (v1 schema)

```jsonc
{
  "name": "code-review",
  "version": 3,                      // server-assigned, monotonic per name
  "params": {                        // JSON-schema-ish typed params
    "repo_url": {"type": "string", "required": true},
    "focus":    {"type": "string", "default": "correctness"}
  },
  "envs": {
    "main": {"provider_instance": "default", "image": "...", "setup": ["git clone {{.repo_url}} ."]}
  },
  "agents": {
    "reviewer": {
      "model": {"provider": "anthropic", "id": "...", "fallbacks": []},
      "prompt": "Review {{.repo_url}} focusing on {{.focus}}...",
      "system_prompt_ref": "default-v1",
      "tools": ["bash", "view", "grep", "session_memory_set", "spawn_agent"],
      "envs": ["main"],
      "entry": true                  // started at invoke time
    },
    "security-checker": {            // spawnable by reviewer, not auto-started
      "model": {...}, "prompt": "...", "tools": ["bash", "view"], "envs": ["main"]
    }
  }
}
```

- **Validation at Put time** (structured field errors, never at invoke): template
  syntax (`text/template` parse), param references resolve, tool names exist in the
  registry, env refs resolve, provider-instance refs are late-bound by name (validated
  at invoke against the org's registered instances, so flows are portable across
  orgs), entry agents exist, grant sets are well-formed, model configs resolvable.
  Invalid → 400 with a path-addressed error list
  (`agents.reviewer.tools[3]: unknown tool "bsh"`).
- **Spawnable-but-not-entry agents**: a flow-scoped agent catalog; `spawn_agent` from
  within an invocation accepts `agent_ref: "security-checker"` and inherits that
  definition (still subject to the narrowing lattice — the flow definition is the
  ceiling, the parent's grants clamp it).

### Invocation

`InvokeFlow{name, version?, session_id | new_session, params}`:

1. Resolve version (latest if unspecified) and validate params against the schema.
2. Tx: create invocation row, append `FlowInvoked`, create env rows + provision jobs
   for all declared envs, create entry runs (state `pending`, prompt rendered with
   params, grants from definition, `flow_invocation_id` set) with first-step jobs
   **gated on env readiness**: entry runs whose envs aren't ready yet start in a
   `pending` hold; the env-ready transition hook enqueues their first step (same
   completion-hook mechanism as `wait_for_agents`).
3. Return `{invocation_id, session_id, run_ids, event_seq}`.

Rendered prompts are persisted on the run (not re-rendered), so A4.2's pinning is
trivially true and auditable.

### CLI (`cmd/ultra`)

Thin wrapper over the generated Go client (same artifact as the testclient — the CLI is
another consumer, not a backdoor). `ultra flow invoke ... --follow` tails the session
event stream to the terminal, rendering the same typed events the SPA renders.

## Work breakdown

1. Definition schema + validator + tests (table-driven over invalid fixtures).
2. Migrations + store for flows/invocations; proto additions + codegen.
3. PutFlow/GetFlow/ListFlows with server-assigned versioning.
4. InvokeFlow: param validation, rendering, transactional multi-entity creation,
   env-readiness gating.
5. `agent_ref` spawning from the flow catalog.
6. CLI: put/list/invoke/--follow.
7. UI: catalog, schema-derived invoke form, provenance badges.
8. Tests A4.1–A4.5.

## Acceptance tests

- **A4.1 — Single-agent flow end-to-end.** Put flow v1 (one entry agent + one env) →
  InvokeFlow into an existing session with `repo_url` param. Assert: `FlowInvoked`
  event; env provisioned per spec (setup command ran — file check in container); run
  started with the rendered prompt (exact string, param interpolated) only after env
  ready; run, env, and events all carry `flow_invocation_id`.
- **A4.2 — Version pinning.** Start a v1 invocation with a slow script mid-run; Put v2
  with a changed prompt. Assert: the in-flight run's persisted prompt is the v1
  rendering; a new invocation (unpinned) uses v2; `GetFlow(name, 1)` still returns v1
  verbatim; invocations record their exact version.
- **A4.3 — Multi-agent topology.** Flow wires an entry supervisor (with
  `spawn_agent` grant + `agent_ref` catalog) and two spawnable workers with distinct
  env specs. One invocation → supervisor spawns both via `run_agent_cohort` with
  `agent_ref`s → full tree completes with zero client-side orchestration. Assert tree
  shape, per-worker env isolation, cohort results in supervisor history.
- **A4.4 — Validation is a wall.** Table test: template syntax error, unknown tool,
  dangling env ref, param type mismatch, missing entry agent, grant exceeding
  catalog ceiling — each Put rejected with a structured, path-addressed field error;
  nothing persisted.
- **A4.5 — Playwright golden.** Browse the catalog → open invoke form (fields derived
  from param schema, defaults prefilled) → invoke → land in the session and watch the
  multi-agent run unfold live → run cards show flow name + version provenance →
  invoke again from CLI (`ultra flow invoke --follow`) and see both invocations listed
  in the UI.

## Exit criteria

- A4.1–A4.5 green in CI.
- `docs/flows.md`: definition schema reference + two example flows checked into
  `examples/flows/` and used as test fixtures (docs proven by tests).
- CLI released as part of the standard build.
