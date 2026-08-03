# Phase E0 — behavior-to-evidence inventory

E0 adds no product behavior. Its acceptance is meta: baseline measurement,
exhaustive extraction inventory, and a CI fence that later phases arm.

| # | Behavior / acceptance bullet | Evidence | Status |
|---|---|---|---|
| A0.1 | Pre-extraction baseline recorded at `16629b5` with build/lint/test/functional/codegen/coverage + LOC | `agent_docs/core_extraction_plan/baseline.md` | done (see baseline for any env notes) |
| A0.2a | Inventory covers 100% of root domain exported types | `extraction_inventory.md` §1 (106 root + 4 secrets types; review walk) | done |
| A0.2b | Inventory covers packages listed in phase plan | `extraction_inventory.md` §2 (`go list` tops dispositioned) | done |
| A0.2c | Inventory covers all `proto/ultra/v1/*.proto` | `extraction_inventory.md` §3 (7 files) | done |
| A0.2d | Inventory covers migrations 00001–00009 | `extraction_inventory.md` §4 (9 files) | done |
| A0.2e | Inventory covers `CanonicalTools()` | `extraction_inventory.md` §5 (29 tools) | done |
| A0.2f | Inventory covers every `EventKind*` | `extraction_inventory.md` §1.4 / §6 (35 kinds; keep 21) | done |
| A0.2g | Inventory covers every `e2e/*.go` + `coverage.json` caps | `extraction_inventory.md` §7 (25 files, 27 caps, 9 deferred, named UI specs) | done |
| A0.2h | Zero rows marked TBD; ambiguities resolved | `extraction_inventory.md` §8 | done |
| A0.2i | Spot-check: flowdef drop/E1; envwork generalize/E2; jobqueue/river keep; hosted.go drop/E1; automation keep | `extraction_inventory.md` §9 | done |
| A0.3a | Fence script exists | `scripts/check-extraction-fences.sh` | done |
| A0.3b | Fence reads per-phase files under `fences/eN.txt` | script + `fences/README.md` | done |
| A0.3c | Fence wired into `task lint` | `Taskfile.yml` lint cmds | done |
| A0.3d | Fence passes with no active terms | `task lint` / direct script run | done |
| A0.3e | Fence demonstrably fails when a banned term is planted, then reverts clean | `phase_e0_audit.md` fence proof | done |
| A0.4 | No production Go/proto/sql behavior changes beyond scripts + Taskfile | `git diff 16629b5 -- '*.go' '*.proto' '*.sql'` empty of product edits | done |
| T0.4 | Worktree tracks `core-extraction`; phase inventory + audit pair exists | branch check + this file + `phase_e0_audit.md` | done |

## Out of scope (explicit non-goals)

- No domain type deletions or renames (E1+).
- No proto reshape (E4).
- No consumer migrations (E5/E6).
- No active fence terms yet (E1 appends the first ban list).
