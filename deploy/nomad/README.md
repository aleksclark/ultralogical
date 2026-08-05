# Nomad deployment contract (ultracore)

Authoritative jobspec for the **ultracore** product shipped from
`aleksclark/ultralogical`.

## Layout

| Path | Role |
|---|---|
| `deployment.yaml` | Value-free project/fleet contract |
| `ultracore.nomad.hcl` | Live job definition (api/worker/admin) |
| `images.lock.hcl` | Immutable image tag + digest lock |
| `tests/` | Static contract tests (no secrets, no Nomad) |

## Secrets

Create Nomad Variable path `nomad/jobs/ultracore` with keys only:

- `database_url`
- `master_key`
- `admin_token`
- `admin_token_role`
- `admin_cursor_secret`

Values never belong in git. The jobspec loads them via `nomadVar` templates.

## Image authority

1. Release workflow publishes `ghcr.io/aleksclark/ultracore:<calver>` and emits a digest.
2. Follow-up pin PR rewrites the jobspec image lines to `...@sha256:…`.
3. Deploy the pinned jobspec. Never treat `:latest` as deploy authority.

## Health

| Service | Check |
|---|---|
| cored | `GET /readyz` |
| coreworker | `GET /readyz` |
| coreadmin | `GET /readyz` |

Liveness: `GET /healthz`.

## Ownership

- Application code + image + this jobspec: **project-owned** (`aleksclark/ultralogical`).
- Fleet ledger points here; fleet-iac does not keep a second deployable copy.
- Trusted fleet GHA wrappers are **fleet-owned job only** today; project deploys use a reviewed manual plan+CAS path (or a future project-source extension).
