# Admin API Playwright e2e (Phase E5)

API-level tests against a real `coreadmin` process. **No SPA** — E6 owns the UI.

## Run

From the repo root:

```sh
task admin:e2e
```

This boots disposable Postgres + `coreadmin` (+ `cored` for isolation checks),
seeds fixtures, runs Playwright, and tears everything down.

## Layout

| Path | Role |
|---|---|
| `tests/admin-api.spec.ts` | Cases: auth, healthz, pagination, limits, lists, health, secrets, cored 404 |
| `src/client.ts` | `@connectrpc/connect` client over generated `AdminReadService` |
| `src/endpoints.ts` | Loads endpoint JSON written by the stack script |
| `src/gen` | Symlink to `clients/admin-ts/src/gen` (private admin TS client) |
| `cmd/seed` | Go seeder invoked by the stack bootstrap |
| `../scripts/admin-e2e-stack.sh` | Bootstrap / run / teardown |

## Debug

```sh
bash scripts/admin-e2e-stack.sh boot          # leave stack up; prints JSON path
bash scripts/admin-e2e-stack.sh run <json>    # re-run tests only
```
