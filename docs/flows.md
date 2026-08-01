# Flows

A flow is an org-scoped, immutable, versioned declaration of the work an
invocation performs: its typed parameters, the environments it provisions, and
the agent topology it starts. Invoking a flow produces a durable invocation
whose provisioning, readiness gating, topology, cleanup, and cancellation are
persisted state, not handler-local orchestration.

Everything a flow can reach it must declare. An agent receives exactly the
environments named in its declaration and exactly the tools it lists; there is
no blanket authority for flow-started agents.

## Definition language (v1)

```jsonc
{
  "description": "Operator documentation; ignored by execution.",

  "params": {
    "subject": {"type": "string", "required": true, "description": "..."},
    "depth":   {"type": "number", "default": 2},
    "verbose": {"type": "boolean", "default": false}
  },

  "envs": {
    "main": {
      "provider_instance": "default",   // late-bound by name at invoke time
      "image": "...",                   // optional; provider default otherwise
      "workdir": "/work",
      "env": {"SUBJECT": "{{.subject}}"},
      "metadata": {"team": "platform"},
      "setup": ["git clone {{.subject}} ."],
      "readiness": "health",            // health (default) | none
      "required": true,                 // default true
      "timeout": "5m"
    }
  },

  "agents": {
    "reviewer": {
      "prompt": "Review {{.subject}} at depth {{.depth}}",
      "model": {"provider": "openai", "model_id": "...", "credential": "default"},
      "tools": ["view", "grep", "spawn_agent"],
      "envs": ["main"],
      "entry": true,                    // started when readiness passes
      "may_spawn": true,
      "max_children": 4
    },
    "summarizer": {
      "prompt": "Summarize {{.subject}}",
      "after": ["reviewer"],            // starts once every dependency is terminal
      "tools": ["post_event"]
    },
    "security": {
      "prompt": "Audit {{.subject}}",
      "spawnable": true,                // catalog-only; not auto-started
      "tools": ["view"]
    }
  }
}
```

Templates are Go `text/template` over the resolved parameters, and may appear in
agent prompts and in an environment's image, workdir, env values, metadata
values, and setup commands.

## Validation

Validation happens before anything is persisted, at `PutFlow` and at
`ValidateFlow`, and produces an ordered list of typed field errors:

```json
{"path": "agents.reviewer.tools[3]", "code": "unknown_tool", "message": "unknown tool \"bsh\""}
```

Codes are stable and machine-readable; messages are for humans. The rejected
classes are: malformed JSON, unknown fields, duplicate names, invalid names,
missing required fields, unknown parameter/env/agent/tool references, invalid
templates, invalid or mismatched parameter types, missing entry agents,
dependency cycles, unreachable agents, ill-formed grants, invalid durations,
unsupported readiness policies and providers, and provider capabilities an org's
registrations cannot satisfy.

## Versions

Versions are immutable. `PutFlow` with `version: 0` assigns the next version;
`PutFlow` with an explicit existing version is rejected with `already_exists`
and changes nothing. Concurrent unversioned writes to the same name converge on
distinct ascending versions, and every version reads back byte-for-byte.
`GetFlow` with version `0` resolves the latest.

## Invocation lifecycle

`InvokeFlow` validates the definition, resolves parameters, renders every
template, and persists the rendering with the invocation. A later flow version
therefore cannot change what an in-flight or replayed invocation does.

```
pending → provisioning → running → completed
                      ↘ cancelling ↘ failed | cancelled
```

An invocation advances through a durable, self-rescheduling queue job:

1. **accepted** — the invocation, its `flow_invoked` event, and its first
   advance job commit together.
2. **provisioning** — every declared environment is created exactly once,
   carrying the invocation id and its declaration name.
3. **readiness** — no agent starts until every required environment is ready and
   its setup commands have run. A failed required environment fails the
   invocation.
4. **running** — declared stages start in dependency order. Agents sharing a
   stage share a cohort id and keep their declaration ordinals.
5. **terminal** — the invocation converges with a typed reason: `completed`,
   `environment_failed`, `agent_failed`, `cancelled`, `invalid_definition`,
   `timed_out`, or `internal`.

Progress is append-only and keyed, so a redelivered advance records nothing
twice, and each step is mirrored into the session log as
`flow_invocation_progressed`. Replaying the log from seq 0 reconstructs the same
ordered history the API reports.

Every stage has its own deadline, and an invocation has an outer one
(`ULTRA_FLOW_INVOCATION_TIMEOUT`, one hour by default): an invocation always
converges and always releases what it owns, rather than retrying forever.

Cleanup releases exactly the resources an invocation owns, identified by the
invocation id persisted on each row. A session's other environments and runs are
never touched, and retrying creates no duplicates.

## Provenance

Every run and environment a flow creates carries `flow_invocation_id` plus the
declaration name (`flow_agent_name` / `flow_env_name`) permanently. `GetRun`,
`ListEnvs`, and `GetFlowInvocation` all expose it, and the log carries it in
`flow_invoked`.

## Catalog spawning

An agent marked `spawnable` is published to the invocation's catalog. A running
agent with the `spawn_agent` tool can launch it by name:

```json
{"agent_ref": "security"}
```

The server supplies the declared prompt, tools, and environments, so a model
cannot widen a catalog agent by describing it differently, and the parent's
grants still clamp the result. Referencing an agent that is not published is
refused exactly like one that does not exist.

## CLI

```sh
ultra flow validate -f flow.json --json
ultra flow put review -f flow.json
ultra flow list
ultra flow get review --version 1
ultra flow versions review
ultra flow invoke review --session "$SESSION" --param subject=database --wait
ultra flow status "$INVOCATION" --json
ultra flow cancel "$INVOCATION"
```

Every command talks to the public API, supports `--json`, and exits nonzero on a
typed failure. `ULTRA_URL`, `ULTRA_TOKEN`, and `ULTRA_ORG` configure it.

## Examples

Three executable examples ship in `examples/flows/` and are run against the real
stack by the acceptance suite:

- `single-agent.json` — one agent, no environment.
- `environment-backed.json` — one agent inside an environment the flow
  provisions, with setup commands and a readiness gate.
- `multi-agent.json` — a planner, two parallel workers, and a summarizer.
