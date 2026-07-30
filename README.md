# Ultralogical

A durable-session platform for agentic work. Sessions span applications and
environments, provide relevant context by default, expose structured data and
actions, preserve history, and can be driven by software while remaining
visible and controllable by people.

Multi-tenant from day 1: orgs bring their own inference credentials
(OpenAI/Anthropic/Bedrock) and choose where dev environments run — hosted
EKS, their own k8s/nomad clusters, or their own machines via a tunneled local
provider.

## Status

**Phase 1 complete**: schema-first ConnectRPC API (Go + TS generated
clients), tenant-scoped Postgres store, gapless session event log, durable
fantasy agent loop (one River job per step, crash-resumable history), BYO
OpenAI/Anthropic/Bedrock credentials encrypted at rest, ask-user workflows,
streaming React SPA, and real-stack API + Playwright acceptance suites.

See [`plan/index.md`](plan/index.md) for the architecture and roadmap, and
[`AGENTS.md`](AGENTS.md) for the contributor/agent cheatsheet.

## Quick start

```sh
task dev               # local postgres (docker) + ultrad on :8080
task test:all          # full test suite (requires docker)
```
