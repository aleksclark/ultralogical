# Extraction baseline

Frozen pre-extraction evidence at the Phase 10 merge commit. Every later
"did we regress?" question answers against this file.

## Identity

| Field | Value |
|---|---|
| Commit | `16629b5` (`16629b5832b9daf40791fd2c0c09b170c9202b18`) |
| Subject | `Merge pull request #21 from aleksclark/aleks/phase-10-static` |
| Branch | `core-extraction` |
| Module | `github.com/aleksclark/ultracore` |
| Go version (baseline host) | `go1.26.5 linux/amd64` |
| Recorded at | 2026-08-03T16:02:38-05:00 (E0) |
| Host notes | Docker available; `golangci-lint` 2.11.3; `task` 3.52.0; `buf` 1.72.0 |

## Commands and results

All commands run from the worktree root against tree content at `16629b5`
(E0 only adds docs/scripts/Taskfile wiring; product packages unchanged).

| Command | Exit | Wall time | Notes |
|---|---|---|---|
| `task build` | **0** | **4s** | `go build ./...` |
| `task lint` | **0** | **10s** (pre-fence) / **1s** cached (with fence) | `buf lint` + `go vet` + `golangci-lint run` (0 issues). Post-E0 also runs `scripts/check-extraction-fences.sh` → `extraction fences: no active terms (E0 baseline); ok`. |
| `task test` | **0** | **51s** | Unit + store + queue + provider conformance (excludes `e2e/`). All packages green. |
| `task test:functional` | **0** | **504s** | `go test ./e2e/ -count=1 -timeout 40m` → `ok github.com/aleksclark/ultracore/e2e 503.358s` |
| `task verify:codegen` | **0** | **~1s** | Go codegen match + rust coverage (7 protos, 151 messages, 8 services). TS typecheck skipped: `clients/ts` deps not installed (`npm ci` would enable it). |
| `task verify:coverage` | **0** | **1s** | `27 capabilities have validated, CI-executed web and GPUI evidence; 40/49 RPCs covered, 9 explicitly deferred` |

### `task test` package results

```
ok  github.com/aleksclark/ultracore                          0.005s
ok  github.com/aleksclark/ultracore/cmd/core/cli            17.521s
ok  github.com/aleksclark/ultracore/envprovider               0.008s
ok  github.com/aleksclark/ultracore/envprovider/k8s          49.243s
ok  github.com/aleksclark/ultracore/envprovider/localdocker   5.197s
ok  github.com/aleksclark/ultracore/envprovider/nomad         8.447s
ok  github.com/aleksclark/ultracore/envprovider/static        4.963s
ok  github.com/aleksclark/ultracore/envprovider/tunnel       18.439s
ok  github.com/aleksclark/ultracore/http                      0.012s
ok  github.com/aleksclark/ultracore/jobqueue/inproc           7.349s
ok  github.com/aleksclark/ultracore/jobqueue/river           13.424s
ok  github.com/aleksclark/ultracore/mcp                       0.003s
ok  github.com/aleksclark/ultracore/postgres                  1.686s
ok  github.com/aleksclark/ultracore/secrets                   0.002s
ok  github.com/aleksclark/ultracore/testkit/testclient        0.004s
```

No-test packages (compile-only): `cmd/devstack`, `cmd/core`,
`cmd/core-env-agent`, `cmd/cored`, `cmd/coreworker`, `envprovider/conformance`,
`envwork`, `flowwork`, `gen/...`, `jobqueue`, `jobqueue/conformance`, `loop`,
`testkit/{envconverge,harness,modelscript,pgtest}`.

### `task test:functional`

```
ok  github.com/aleksclark/ultracore/e2e  503.358s
```

Full acceptance suite green on the real stack (harness boots Postgres,
modelscript, cored, worker as required per test).

### `task verify:codegen`

```
rust codegen covers 7 proto files, 151 messages, 8 services
clients/ts deps not installed; run npm ci to include the TS typecheck
```

Go generated output matches protos. TS typecheck is optional when
`node_modules` is absent (existing Taskfile behavior) — not a baseline
failure.

### `task verify:coverage`

```
27 capabilities have validated, CI-executed web and GPUI evidence; 40/49 RPCs covered, 9 explicitly deferred
```

## Lines of code

```sh
find . -name '*.go' -not -path './gen/*' | xargs wc -l | tail -1
# 30280 total
```

| Scope | LOC |
|---|---|
| **Non-generated Go total** | **30280** |
| Root domain `*.go` | 2750 |
| `loop/` | 2294 |
| `envwork/` | 748 |
| `flowwork/` | 879 |
| `envprovider/` | 5767 |
| `jobqueue/` | 865 |
| `postgres/` | 2190 |
| `http/` | 2348 |
| `mcp/` | 418 |
| `secrets/` | 294 |
| `testkit/` | 1775 |
| `cmd/` | 2498 |
| `e2e/` | 7454 |
| `clients/` (Go) | 0 |
| `ui/` (Go) | 0 |

Generated Go under `gen/` is excluded from the headline count (regenerated
from protos; not a deletion target measured in LOC).

## Environment limits / blockers

- **None for the E0 command set.** Build, lint, unit/conformance, functional,
  codegen, and coverage all exited 0.
- Docker was available; provider conformance (localdocker, k8s, nomad, static,
  tunnel) ran inside `task test` and passed.
- Full kind-cluster provider CI legs (`providers-kubernetes` workflow) and
  `task web:test` / `task desktop:test` were **not** part of the E0 command
  list (UI surfaces delete in E1; kind legs remain CI-only).
- `clients/ts` typecheck skipped without `npm ci` — documented Taskfile
  soft-skip, not a red gate.

## How to reproduce

```sh
git checkout 16629b5
task build && task lint && task test
task test:functional
task verify:codegen && task verify:coverage
find . -name '*.go' -not -path './gen/*' | xargs wc -l | tail -1
```

After E0 lands, `task lint` also runs the extraction fence script (no active
terms until E1 closes).
