# Nomad deployment contract (ultracore)

Authoritative project-owned contract for the **ultracore** product shipped from
`aleksclark/ultralogical`. Plan 03 schema version 1.

## Layout

| Path | Role |
|---|---|
| `deployment.yaml` | Plan 03 source/ownership/reconcile manifest |
| `jobs/ultracore.nomad.hcl` | Live job definition (api/worker/admin) |
| `env/home.nomadvars.hcl` | Non-secret fleet overlay |
| `images.lock.hcl` | Digest-only image authority |
| `tests/` | Static contract tests (no secrets, no Nomad submit) |

## Source enrollment

Fleet source `project-ultralogical` remains **disabled / observe-only** until a
separate fleet-iac PR enables it. This repository does **not** submit Nomad jobs
from GitHub Actions. No live deploy from project workflows.

`ultracore-image-load` is **retired** and must not be enrolled.

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
2. Follow-up pin PR rewrites `jobs/ultracore.nomad.hcl` image lines and `images.lock.hcl`.
3. Deploy authority is `…@sha256:…` only. Never treat `:latest` as authority.

## Health

| Service | Check |
|---|---|
| cored | `GET /readyz` |
| coreworker | `GET /readyz` |
| coreadmin | `GET /readyz` |

Liveness: `GET /healthz`.

## Data / rollback

- Stateful: PostgreSQL-backed (external). Job does not own DB volumes.
- `cored` runs migrations (`CORE_MIGRATE=true`); worker/admin do not.
- Rollback: previous job version / prior digest pin after schema compatibility is proved.
- Do not purge Nomad Variables or DB on rollback.

## Ownership

- Application code + image + this jobspec: **project-owned** (`aleksclark/ultralogical`).
- Fleet ledger must set `canonical_path` to `jobs/ultracore.nomad.hcl` (or
  `deploy/nomad/jobs/ultracore.nomad.hcl` if ledger stores repo-root paths that
  match the deployment `spec` exactly after fleet path-policy update).
- After this PR merges: **separate fleet-iac PR** to update ledger
  `canonical_path` from the pre-move flat path and keep `managed=false`.

## Local validation (no cluster write)

```bash
go test ./deploy/nomad/tests -count=1
bash deploy/nomad/tests/contract.sh
# optional if nomad CLI present:
# nomad fmt -check deploy/nomad/jobs
# nomad job run -output deploy/nomad/jobs/ultracore.nomad.hcl >/dev/null
```
