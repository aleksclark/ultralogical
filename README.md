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

**Phase 6.7 remediation in progress**: the platform includes durable sessions,
agent loops, development environments, multiplayer, flows, and advanced-loop
groundwork, but the independent Phase 0–6 audit found material completion gaps.
`agent_docs/phases_0_6_audit.md` assigns those gaps to completion-scoped
Phases 7–11; production hardening and release proof follow in Phases 12–13.

See [`plan/index.md`](plan/index.md) for the architecture and roadmap, and
[`AGENTS.md`](AGENTS.md) for the contributor/agent cheatsheet.

## Quick start

```sh
task dev               # local postgres (docker) + ultrad on :8080
task test:all          # full test suite (requires docker)
```
