# Deploying ultracore

Stack: **cored** (API) + **coreworker** (loop/resource jobs) + **Postgres**.

## Quick reference

```sh
# docker compose (reference)
docker compose up --build

# health
curl -sf http://localhost:8080/healthz   # liveness
curl -sf http://localhost:8080/readyz   # pg reachable (fails if postgres stopped)
curl -sf http://localhost:8081/readyz   # worker
```

## Configuration (env-only)

All process configuration is environment variables. **`CORE_*` variables that
are not in the known set refuse startup** with
`unknown CORE_* environment variable(s): ...` (config drift fence).

| Variable | Binaries | Default | Purpose |
|---|---|---|---|
| `DATABASE_URL` | both | _(required)_ | Postgres DSN |
| `CORE_MASTER_KEY` | both | _(required)_ | 32-byte hex AES key for credentials/API keys |
| `CORE_ADDR` | cored / worker | `:8080` / _(unset)_ | API listen addr; on worker, opt-in health listen addr |
| `CORE_MIGRATE` | cored | `true` | Run goose migrations at startup |
| `CORE_DEFAULT_PROVIDER` | cored | `openai` | Default model provider for StartRun |
| `CORE_DEFAULT_MODEL` | cored | `gpt-4.1-mini` | Default model id |
| `CORE_JOB_TIMEOUT` | worker | `2m` | Per-job timeout |
| `CORE_RESCUE_AFTER` | worker | job+30s | Rescue stuck jobs after |
| `CORE_MAX_WORKERS` | worker | `10` | Per-process queue concurrency |
| `CORE_RECONCILE_INTERVAL` | worker | `5s` | Resource reconcile tick |
| `CORE_PROVISION_TIMEOUT` | worker | `1m` | Provision deadline |
| `CORE_BEZALEL_IMAGE` | both | `ultracore/bezalel:local` | Tool-endpoint image |
| `CORE_BEZALEL_BINARY` | both | _(empty)_ | Host path override for bezalel binary |
| `CORE_K8S_ENDPOINT_MODE` | both | _(empty)_ | k8s endpoint mode |
| `CORE_K8S_ENDPOINT_HOST` | both | _(empty)_ | k8s endpoint host |
| `CORE_K8S_NODEPORT_LOW` | both | _(empty)_ | NodePort range low |
| `CORE_K8S_NODEPORT_HIGH` | both | _(empty)_ | NodePort range high |
| `CORE_PROVIDER_KINDS` | both | _(all)_ | Comma-separated enabled provider kinds |
| `CORE_OTLP_ENDPOINT` | both | _(empty)_ | OTLP collector endpoint (tracing) |
| `CORE_LOG_LEVEL` | both | _(default)_ | Reserved log level knob |
| `CORE_URL` | CLI | `http://localhost:8080` | CLI API base |
| `CORE_TOKEN` | CLI | _(required)_ | CLI bearer token |
| `CORE_TENANT` | CLI | _(optional)_ | Default tenant id |

Unknown `CORE_*` → process exits non-zero before opening sockets.

## Health endpoints

| Path | Meaning |
|---|---|
| `GET /healthz` | Process alive |
| `GET /readyz` | Postgres ping succeeds (cored also implies queue handle constructed) |

When Postgres is stopped, `/readyz` returns HTTP 503. Load balancers should
gate traffic on `/readyz`, not `/healthz`.

`coreworker` only binds health endpoints when `CORE_ADDR` is set (compose and
Nomad set `:8081`). Bare test workers leave it unset so parallel suites do not
collide on a fixed port.

## Local dev stack

`task dev` / `scripts/dev-stack.sh` boots Postgres, a local OpenAI-compatible
model, cored, and coreworker from the squashed baseline. Seed prints
`tenant_id` + `api_key` JSON; smoke authenticates with that API key (there is
no dev-token authenticator). `task dev:smoke` is the noninteractive proof.

## OTLP tracing

Set `CORE_OTLP_ENDPOINT` (e.g. `http://otel-collector:4317`) to export spans
for sessions, steps, and tool calls from the owned loop instrumentation.
When unset, tracing is a no-op.

## Docker

- **Image:** multi-binary (`cored`, `coreworker`, `core` CLI). See root
  `Dockerfile`.
- **Compose:** `docker-compose.yml` boots Postgres + both binaries.
- Tag images as `ghcr.io/aleksclark/ultracore:0.1.0` (or your registry).

## systemd

Unit examples live in `deploy/systemd/`. Provide
`/etc/ultracore/cored.env` and `coreworker.env` with the table above.

## Nomad

`deploy/nomad/ultracore.nomad.hcl` is a starting point for a two-group job
(api + worker) with HTTP checks on `/readyz`.

## Migrations

Goose baseline is `postgres/migrations/00001_baseline.sql`. After E4, schema
changes are **additive-only**. River queue tables are created by River's own
migrator when a process opens the queue.


## coreadmin (private operator API)

`coreadmin` is a separate process from `cored`. Bind it privately
(`CORE_ADMIN_ADDR`, default `127.0.0.1:8082`), set `CORE_ADMIN_TOKEN`, and do
not attach public Traefik routes. See [admin-api.md](admin-api.md).
