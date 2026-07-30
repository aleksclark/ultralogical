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

**Phase 0 complete**: schema-first ConnectRPC API (Go + TS generated
clients), tenant-scoped Postgres store, gapless per-session event log with
live fan-out, transactional job-queue seam (river + inproc, shared
conformance suite), real-stack functional test harness, CI drift gates.

See [`plan/index.md`](plan/index.md) for the architecture and roadmap, and
[`AGENTS.md`](AGENTS.md) for the contributor/agent cheatsheet.

## Quick start

```sh
task dev               # local postgres (docker) + ultrad on :8080
task test:all          # full test suite (requires docker)
```
