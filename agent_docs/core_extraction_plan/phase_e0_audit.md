# Phase E0 — completion audit

Independent check that every E0 acceptance bullet is closed. No open scoped
bullets → E1 may start.

## Branch / worktree hygiene (T0.4)

| Check | Result |
|---|---|
| Current branch | `core-extraction` |
| HEAD | `16629b5` (`16629b5832b9daf40791fd2c0c09b170c9202b18`) |
| Tracks extraction branch (not master) | yes — local branch `core-extraction` at Phase 10 merge baseline |
| Phase inventory + audit pair | `phase_e0_inventory.md`, this file |

## A0.1 — Baseline

| Check | Result |
|---|---|
| `baseline.md` exists | yes |
| Commit recorded | `16629b5` |
| `task build` green + runtime | 0 / 4s |
| `task lint` green + runtime | 0 / 10s at baseline host (0 golangci issues). Review host re-ran fence script + `buf lint` + `go vet` green; `golangci-lint` binary absent on review host only (not a baseline regression). |
| `task test` green + runtime | 0 / 51s |
| `task test:functional` green + runtime | 0 / 504s (`e2e` 503.358s) |
| `task verify:codegen` green | 0 / ~1s (TS soft-skip without node_modules) |
| `task verify:coverage` green | 0 / 1s (27 caps validated) |
| LOC recorded | **30280** non-generated Go |
| Environment blockers | none for the E0 command set |

## A0.2 — Extraction inventory

| Check | Result |
|---|---|
| File | `extraction_inventory.md` |
| Root domain types | §1 covers ultra/run/env/event/flow/flowdef/multiplayer/automation/credential/capability/auth/store/secrets |
| Packages | §2 covers loop, envwork, flowwork, envprovider/*, jobqueue/*, postgres, http, mcp, secrets, testkit/*, clients/*, cmd/*, ui/* |
| Protos | §3 all seven `proto/ultra/v1/*.proto` |
| Migrations | §4 `00001`–`00009` |
| Loop tools | §5 full `CanonicalTools()` list (29 tools) |
| Event types | §1.4 / §6 every `EventKind*` (**35** total; keep 21 / gen 8 / drop presence 3 / drop flow 3) |
| e2e + coverage | §7 all 25 `e2e/*.go` files + 27 capability keys + 9 deferred RPCs + named UI specs |
| Zero TBD | confirmed — ambiguities locked in §8 |
| Spot-check `flowdef.go` | drop / E1 |
| Spot-check `envwork/` | generalize / E2 |
| Spot-check `jobqueue/river` | keep |
| Spot-check `envprovider/k8s/hosted.go` | drop / E1 |
| Spot-check `automation.go` | keep |
| Tree-walk method documented | §10 |

### Independent tree walk (not inventory-first)

Re-checked by walking the live tree at `16629b5` and only then grepping the
inventory for each discovered name:

| Axis walked | Live count | Inventory coverage |
|---|---|---|
| Root `*.go` files | 14 (`auth`, `automation`, `capability`(+test), `credential`, `env`, `event`, `flow`, `flowdef`(+test), `multiplayer`, `run`, `store`, `ultra`) | §1 source list + whole-file flow/flowdef |
| Root non-test exported `type` decls | 106 | every name present (flow/flowdef covered by whole-file rows) |
| `secrets/` exported types | 4 (`Keyring`, `AESKeyring`, `Redactor`, `RedactingHandler`) | §1.12 |
| `go list ./...` top components | `(root)`, cmd, e2e, envprovider, envwork, flowwork, gen, http, jobqueue, loop, mcp, postgres, secrets, testkit | §2 + gen/e2e ancillary |
| `proto/ultra/v1/*.proto` | 7 | §3 |
| `postgres/migrations/*.sql` | 9 (`00001`–`00009`) | §4 |
| `CanonicalTools()` strings | 29 | §5 |
| `EventKind*` constants | 35 | §1.4 / §6 (bucket counts corrected on review) |
| `e2e/*.go` | 25 | §7.1 |
| `coverage.json` capabilities | 27 | §7.2 |
| `coverage.json` deferred | 9 full `ultra.v1.*` keys | §7.4 |
| `ui/web/e2e/*.spec.ts` | 9 named files | §7.3 |
| `ui/desktop/tests/*_e2e.rs` | 5 + `support/mod.rs` | §7.3 |
| Spot-checks | flowdef drop/E1; envwork generalize/E2; jobqueue/river keep; hosted.go drop/E1; automation keep | §9 |

Review corrections applied into the inventory (still zero TBD):

- Named every `ProviderCapability` constant (hosted isolation/quota → drop/E1).
- Named `Grants.AllowsEnv` (drop/E1) and Actor/wait/memory/run/env constants.
- Fixed §6 keep-bucket count **22 → 21** (35 total).
- Expanded deferred RPCs and UI e2e specs to explicit filenames/keys.
- Dispositioned ancillary roots (`go.mod`, buf, Taskfile, workflows, docs).

## A0.3 — Term fences

| Check | Result |
|---|---|
| Script | `scripts/check-extraction-fences.sh` (executable) |
| Fence dir + format docs | `agent_docs/core_extraction_plan/fences/README.md` |
| Active terms at E0 | none (no `eN.txt` ban files) — script prints `no active terms (E0 baseline); ok` |
| Wired into `task lint` | yes — final lint cmd runs the script |
| `task lint` includes fence step | observed output: `task: [lint] bash scripts/check-extraction-fences.sh` → ok |

### Fence failure proof (plant → fail → revert)

Original E0 demonstration plus a review re-run. Review re-run transcript:

```sh
# 1. Activate a ban term
printf '%s\n' 'PlantedFenceTermXYZ' > agent_docs/core_extraction_plan/fences/e0-proof.txt

# 2. Plant the term in tracked live code
printf '\n// PlantedFenceTermXYZ proof marker for E0 fence demonstration\n' >> store.go

# 3. Expect failure
bash scripts/check-extraction-fences.sh; echo exit:$?
# extraction fences: scanning for: PlantedFenceTermXYZ
# extraction fence violated; banned terms still present in live code:
# store.go:<line>:// PlantedFenceTermXYZ proof marker for E0 fence demonstration
# exit:1

# 4. Revert plant + ban file
git checkout -- store.go
rm -f agent_docs/core_extraction_plan/fences/e0-proof.txt

# 5. Expect clean baseline
bash scripts/check-extraction-fences.sh; echo exit:$?
# extraction fences: no active terms (E0 baseline); ok
# exit:0

# 6. Confirm no residue
git diff -- store.go   # empty
ls agent_docs/core_extraction_plan/fences/   # README.md only
```

`task lint` also runs the script as its final step (observed
`task: [lint] bash scripts/check-extraction-fences.sh` → ok).

## A0.4 — No production behavior changes

```sh
git diff 16629b5 --name-only -- '*.go' '*.proto' '*.sql'
# (empty)
```

| Path | Change kind |
|---|---|
| `Taskfile.yml` | lint wiring only (+ fence step) |
| `scripts/check-extraction-fences.sh` | new fence script |
| `agent_docs/core_extraction_plan/**` | plan docs, inventory, baseline, fences README, audits |
| `.tool-versions` | local asdf pins for reproducible tool resolution (not product code) |

No `*.go` / `*.proto` / `*.sql` product edits. Fence proof mutation to
`store.go` was reverted before close.

## Exit criteria

| Criterion | Met? |
|---|---|
| Inventory exhaustive by tree walk | yes |
| Baseline reproducible / green | yes |
| Fences live in `task lint` | yes |
| Fence demonstrably fails on planted term | yes |
| No open E0 scoped bullets | yes |

**E1 may start.**

## Artifacts created this phase

- `agent_docs/core_extraction_plan/baseline.md`
- `agent_docs/core_extraction_plan/extraction_inventory.md`
- `agent_docs/core_extraction_plan/phase_e0_inventory.md`
- `agent_docs/core_extraction_plan/phase_e0_audit.md` (this file)
- `agent_docs/core_extraction_plan/fences/README.md`
- `scripts/check-extraction-fences.sh`
- `Taskfile.yml` lint → fence wiring
- `.tool-versions` (golang / task / nodejs pins)
