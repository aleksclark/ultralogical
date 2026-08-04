# Phase E0 — Baseline, inventory, and extraction fences

**Objective:** Freeze a known-good baseline, produce the authoritative
keep/drop inventory, and install the CI fences that make the later deletion
phases verifiable instead of aspirational.

**Duration guess:** 2–4 days. No behavior changes; this phase is measurement
and scaffolding only.

---

## Scope

- Record the pre-extraction baseline (test results, coverage matrix, LOC).
- Produce `extraction_inventory.md`: every root domain type, package,
  proto file, migration, handler, loop tool, and e2e test tagged
  **keep / generalize / drop / move-to-consumer**.
- Add the "term fence" CI check that later phases use to prove deletions.
- Branch/worktree hygiene so E1's mass deletion is easily reviewable.

## Work items

### T0.1 — Baseline evidence

Run and record into `agent_docs/core_extraction_plan/baseline.md`:

```sh
task build && task lint && task test          # all green, note runtimes
task test:functional                          # all green, note runtimes
task verify:codegen && task verify:coverage   # green
find . -name '*.go' -not -path './gen/*' | xargs wc -l | tail -1   # ~30k
```

Record the closing commit (`16629b5`, phase-10 merge) as the extraction
baseline. Any later "did we regress?" question is answered against this file.

### T0.2 — Extraction inventory

Create `agent_docs/core_extraction_plan/extraction_inventory.md` with one
table per axis. Every row gets a disposition and the phase that executes it:

| Axis | Source of rows |
|---|---|
| Root domain types | `ultra.go`, `run.go`, `env.go`, `event.go`, `flow.go`, `flowdef.go`, `multiplayer.go`, `automation.go`, `credential.go`, `capability.go`, `auth.go`, `store.go`, `secrets/` |
| Packages | `loop/`, `envwork/`, `flowwork/`, `envprovider/*`, `jobqueue/*`, `postgres/`, `http/`, `mcp/`, `secrets/`, `testkit/*`, `clients/*`, `cmd/*`, `ui/` |
| Protos | `proto/ultra/v1/*.proto` (agent, automation, env, event, flow, org, session) |
| Migrations | `postgres/migrations/00001–00009` |
| Loop tools | `ultra.CanonicalTools()` in `multiplayer.go:45` |
| Event types | `event.go` event vocabulary |
| e2e tests | the 25 files in `e2e/`, cross-referenced with `e2e/coverage.json` |

Known dispositions to encode (from the decision record; the inventory makes
them exhaustive):

- **Keep:** event log (`event.go`, `postgres/eventbus.go`), run + loop
  (`run.go`, `loop/` minus flow/presence coupling), jobqueue (all three
  subpackages), store seam (`store.go`, `postgres/`), secrets, mcp cache,
  env lifecycle (`env.go`, `envwork/`) pending E2 generalization,
  providers `localdocker`, `byo_k8s`, `byo_nomad`, `static`, `tunnel` +
  conformance + registry/wiring/capability probing, `testkit/*`,
  `cmd/cored`, `cmd/coreworker`, `cmd/core`, `cmd/devstack`.
- **Drop (E1):** `flow.go`, `flowdef.go`, `flowwork/`, flow protos/handlers/
  tests; presence + human-roster parts of `multiplayer.go`; grants lattice
  (replaced in E3); billing/metering (`env_usage`, `BillingService`,
  `GetUsage`); hosted-EKS isolation (`envprovider/k8s/hosted.go` and the
  namespace/quota/NetworkPolicy machinery); `Org.Plan`; `clients/rust` GPUI
  app, web SPA (`ui/`), their e2e tests and coverage entries; human org
  membership/roles pending E3's service-identity replacement.
- **Generalize (E2):** everything env-named that is actually
  resource-generic.
- **Move-to-consumer:** flows → curri; periodic prompts stay (primer needs
  them — `automation.go` is small and generic); ask_user → stays as an
  event + awaiting state, but UI affordance is consumer-side.

Ambiguous rows get resolved in this phase, in the inventory, not later
mid-deletion. The inventory is the merge-review artifact for E1.

### T0.3 — Term fences

Add `scripts/check-extraction-fences.sh` + CI job. After each phase closes,
its banned terms may not appear in non-doc, non-history code:

```sh
# fence set grows per phase; E1 adds:
banned_e1="flowdef|FlowInvocation|BillingService|env_usage|hosted_eks|Presence|GPUI"
git grep -inE "$banned_e1" -- '*.go' '*.proto' '*.sql' '*.ts' ':!agent_docs' ':!docs' \
  && { echo "extraction fence violated"; exit 1; } || true
```

The script reads a per-phase allowlist file
(`agent_docs/core_extraction_plan/fences/eN.txt`) so closing a phase =
appending its banned terms. Wire into `task lint`.

### T0.4 — Review scaffolding

- Confirm worktree `core-extraction` tracks branch `core-extraction`; all
  extraction phases merge to this branch via PR (never to master) with CI
  green, per repo convention.
- Create `agent_docs/core_extraction_plan/phase_e0_inventory.md` /
  `_audit.md` stubs; each later phase gets the same pair (rule 7).

---

## Acceptance criteria

- **A0.1** `baseline.md` exists and records green `task build`, `task lint`,
  `task test`, `task test:functional`, `task verify:codegen`,
  `task verify:coverage` at commit `16629b5`, with runtimes and LOC counts.
- **A0.2** `extraction_inventory.md` covers 100% of: root `.go` files'
  exported types, packages, proto files, migrations, canonical tools, event
  types, and e2e test files. Zero rows marked "TBD". Spot-check: `flowdef.go`
  → drop/E1; `envwork/` → generalize/E2; `jobqueue/river` → keep;
  `envprovider/k8s/hosted.go` → drop/E1; `automation.go` → keep.
- **A0.3** The fence script exists, runs in `task lint`, currently passes
  (no fences active yet), and demonstrably fails when a banned term is
  planted in a scratch commit (prove once, then revert).
- **A0.4** No production code changed: `git diff 16629b5 -- '*.go' '*.proto'
  '*.sql'` is empty except `scripts/` and Taskfile wiring.

## Test coverage

This phase adds no product behavior; its tests are meta:

| Behavior | Evidence |
|---|---|
| Baseline is green | recorded command output in `baseline.md` |
| Fence catches violations | scratch-commit demonstration recorded in `phase_e0_audit.md` |
| Fence wired into CI | `task lint` output includes the fence step |

## Exit audit

`phase_e0_audit.md` confirms: inventory exhaustive (audited by walking the
tree, not the inventory), baseline reproducible, fences live. No open
bullets → E1 may start.
