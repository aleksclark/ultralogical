# ultracore

Durable-session substrate for agentic work: multi-tenant, event-sourced
sessions with an owned agent loop and pluggable per-tenant resource providers.

Consumers bring their own UI, identity, triggers, and policy.

## What it is

1. **Event-sourced sessions** — append-only, gapless per-session seq, subscribe-from-seq
2. **Owned agent loop** — durable steps on a queue, spawn/wait/cohort, session memory
3. **Session resources** — typed resources behind tenant-scoped provider registrations

## What it is not

Billing, hosted EKS product isolation, flows catalog, multiplayer presence UX,
human user model, first-party web/desktop UI. See
[agent_docs/core_extraction_plan/index.md](agent_docs/core_extraction_plan/index.md).

## Quickstart

```sh
# prerequisites: go 1.25+, docker, task, node 22
task dev          # pg + model + cored + coreworker
task dev:smoke    # boot, smoke, tear down

# Go SDK
import "github.com/aleksclark/ultracore/sdk"

# TS SDK
cd clients/ts && npm ci && npm test
```

## API (proto/core/v1)

| Service | Responsibility |
|---|---|
| `TenantService` | tenants + API keys |
| `CredentialService` | inference credentials |
| `ProviderService` | provider registrations |
| `SessionService` | sessions, labels, memory, archive |
| `RunService` | agent runs (start/answer/cancel/list/tree) |
| `ResourceService` | resource lifecycle + exec-preview |
| `EventService` | append, subscribe, get range |
| `AutomationService` | periodic prompts |

## Layout

```
cmd/cored        API server
cmd/coreworker   queue workers
cmd/core         CLI
sdk/             Go SDK
clients/ts       @ultracore/client (TS SDK)
proto/core/v1    API source of truth
postgres/        store + 00001_baseline.sql
e2e/             functional acceptance suite
```

## Develop

```sh
task build
task lint
task test
task test:functional
task cli:test
task sdk:test
task verify:codegen
task verify:coverage
```

## Deploy

See [docs/deploy.md](docs/deploy.md). Compose: `docker compose up --build`.

## Consumers

See [docs/consumers.md](docs/consumers.md).
