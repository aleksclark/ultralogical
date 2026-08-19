# Stacklane-compatible Docker Compose DEV stack

Parallel worktree-friendly local stack: Postgres + cored + coreworker with Air
hot-reload, published only on `127.0.0.1::<containerPort>` (ephemeral host
ports) with Stacklane labels for stable FQDNs when the Stacklane daemon is
installed.

This path is **additive**. It does **not** replace:

- host `task dev` / `scripts/dev-stack.sh` (Postgres container + host binaries
  + local model + seed)
- legacy `docker compose up --build` against `docker-compose.yml` (fixed
  `5432` / `8080` / `8081` binds, production image)

Those legacy/host flows keep their documented fixed host bindings so existing
scripts and muscle memory still work. They **will collide** if two worktrees
both use the legacy compose file. Use this Stacklane path for isolation.

Stacklane install/daemon is **separate**. This stack works with **direct
loopback ephemeral ports** if the daemon is absent (`stacklane: BLOCKED` in
status is OK). This migration does not mutate daemon, systemd, or host DNS.

## Copy-paste commands

```bash
task dev:compose:check
task dev:compose:up
task dev:compose:status
task dev:compose:endpoints
task dev:compose:logs
task dev:compose:down
CONFIRM=ultracore-<instance>-destroy task dev:compose:destroy
```

Or call the wrapper directly:

```bash
bash scripts/compose-dev.sh check
STACKLANE_INSTANCE=my-feature bash scripts/compose-dev.sh up
```

Default instance is the worktree directory name (e.g. `stacklane-compose-1`),
else branch name, else `dev`. Compose project is always
`ultracore-<instance>` via `docker compose -p` on **every** lifecycle command
(never hardcoded in the YAML).

`logs` follows service output. **Ctrl-C leaves the stack running**; use
`task dev:compose:down` to stop.

`down` never passes `-v`. `destroy` requires
`CONFIRM=ultracore-<instance>-destroy` and then removes named volumes for
**that compose project only**.

## Endpoints

FQDN shape: `<endpoint>.<instance>.ultracore.<base_domain>` (default base
`test`; lifecycle reads `stacklane status` when `STACKLANE_BASE_DOMAIN` is
unset).

| Path | Meaning |
|------|---------|
| `http://api.<instance>.ultracore.test:8080` | cored via Stacklane VIP public port **8080** |
| `http://worker.<instance>.ultracore.test:8081` | coreworker health via VIP **8081** |
| `postgres.<instance>.ultracore.test:5432` | Postgres via VIP **5432** |
| `http://127.0.0.1:<ephemeral>/healthz` | Direct cored liveness |
| `http://127.0.0.1:<ephemeral>/readyz` | Direct cored readiness (Postgres ping) |

Print mappings:

```bash
task dev:compose:endpoints
```

Internal mesh uses Compose DNS (`postgres:5432`), never host ephemeral ports.

## Architecture

- **cored / coreworker** (`Dockerfile.dev`): golang `1.26.6-bookworm` (same
  digest as the production build stage), Air (`github.com/air-verse/air` MIT,
  pinned `v1.67.4`) as PID1, repo bind-mounted at `/src`, named volumes for
  Go mod/build caches and per-service `/src/tmp`.
- **postgres:** `postgres:16-alpine`, data on named volume `ultracore_pgdata`.
- **Publish form:** only `127.0.0.1::<containerPort>`. Never `0.0.0.0`, empty
  host IP, fixed host ports, or host network.
- **Labels:** `stacklane.enable=true`, `stacklane.project=ultracore`,
  `stacklane.instance=${STACKLANE_INSTANCE}`, endpoints `api` / `worker` /
  `postgres`, public ports `8080` / `8081` / `5432`. Container listen port
  equals public port, so `stacklane.target_port` is omitted.
- **Healthchecks:** cored `/readyz` on 8080, coreworker `/readyz` on 8081,
  postgres `pg_isready`.
- **Secrets:** local-only sample `POSTGRES_*` / `CORE_MASTER_KEY` matching the
  existing reference compose so the stack boots without operator tokens. Do
  not reuse outside local compose. No provider API tokens.

There is no first-party browser UI on this stack, so there is no Vite/HMR
surface. Backend Air reload is the reload proof.

## Files

| File | Role |
|------|------|
| `docker-compose.stacklane.yml` | Stacklane DEV compose (this stack) |
| `docker-compose.yml` | Existing reference compose (fixed host ports; unchanged) |
| `Dockerfile.dev` | Air + Go toolchain image |
| `Dockerfile` | Production distroless image (unchanged) |
| `.air.cored.toml` / `.air.coreworker.toml` | Air configs (PID1) |
| `scripts/compose-dev.sh` | Lifecycle: check/up/status/endpoints/logs/down/destroy |
| `scripts/compose-dev_test.sh` | Lifecycle contract tests (no long-running stack) |
| `scripts/compose-dev-proof.sh` | Hot-reload + two-instance isolation proof |

## macOS / Windows

Linux is the primary Stacklane VIP/DNS path. On macOS, direct loopback
Compose still works; VIP proxying may need operator-created `lo0` aliases
(not created by this lifecycle). Windows is unsupported by Stacklane MVP;
use the host `task dev` path or assess Docker Compose separately.
