# ultracore

A durable-session substrate for agentic work — multi-tenant, event-sourced
sessions with an owned agent loop and pluggable per-tenant resource providers.
Go backend, ConnectRPC API, Postgres. Consumers bring their own UI, identity,
triggers, and policy.

## Status

**Core extraction in progress** (`agent_docs/core_extraction_plan/`). Phase E1
shed the product surface (flows, billing, hosted isolation, presence, first-
party UIs, grants lattice) and renamed the module to
`github.com/aleksclark/ultracore`. Later phases generalize resources, reshape
tenancy/identity/policy, and freeze API v1.

See [`AGENTS.md`](AGENTS.md) for the contributor/agent cheatsheet and
[`agent_docs/core_extraction_plan/index.md`](agent_docs/core_extraction_plan/index.md)
for the extraction roadmap.

## Quick start

```sh
task dev               # local postgres (docker) + cored + coreworker
task test:all          # full test suite (requires docker)
```

Binaries: `cored` (API), `coreworker` (queue), `core` (CLI). Configuration uses
the `CORE_*` env prefix (for example `CORE_MASTER_KEY`, `CORE_DEV_TOKENS`,
`CORE_BEZALEL_IMAGE`).
