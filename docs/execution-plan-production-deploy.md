# Execution plan: production deploy contract

Status: in progress (this PR implements the project side).

## Discovery (done)

- Repo: `aleksclark/ultralogical` (public), product name **ultracore**.
- Live Nomad job ID: `ultracore` (api/worker/admin), healthy on fleet.
- Routes: `core.fleet.clark.team`, `core-admin.fleet.clark.team`.
- Health: `/healthz` (live), `/readyz` (ready).
- Image currently: `ghcr.io/aleksclark/ultracore:0.2.0` preloaded locally; GHCR may lack public pull.
- Related helper: `ultracore-image-load` (sysbatch, pending) — retire only after digest pull proof.
- No separate `ultralogical` job ID; do not invent a second service.

## This change

1. Secure Dockerfile (pinned bases, nonroot distroless, `.dockerignore`).
2. Health/config/contract tests.
3. Release workflow: CalVer tag, GHCR push, immutable digest artifact + pin PR.
4. Jobspec: Nomad Variable secrets (no envsubst literals), Traefik internal hosts, `/readyz`.
5. `deployment.yaml` ownership/rollback/secret key contract.

## After merge

1. Wait post-merge CI + release; record digest.
2. Merge digest pin PR.
3. fleet-iac: ledger secret_variable_paths + retirement note for image-load; no duplicate jobspec.
4. Create `nomad/jobs/ultracore` variable keys from live runtime (keys listed only).
5. Reviewed manual plan + CAS update of existing `ultracore` (updates-only; wrapper is fleet-owned-only).
6. Verify health/routes/logs; stop `ultracore-image-load` with explicit tombstone if superseded.
