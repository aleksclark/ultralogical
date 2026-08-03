# Phase E1 — Shed the product surface

**Objective:** Delete everything that is Ultralogical-the-product rather than
the substrate: flows, billing/metering, hosted isolation, multiplayer
presence, the grants lattice, and the first-party UIs. End with a smaller
tree that still passes its (correspondingly smaller) full test suite, then
rename the module.

**Depends on:** E0 (inventory + fences).
**Duration guess:** 1–2 weeks. Deletion order below is chosen so the tree
compiles and tests green after every numbered item.

---

## Scope

Executes every **drop/E1** row of `extraction_inventory.md`. Nothing else.
No generalization (that's E2), no new features. Each deletion item removes:
domain types → store methods + postgres impl → protos + generated code →
http handlers → loop tools → CLI verbs → e2e tests → coverage.json entries →
docs → fence term added.

## Work items

### T1.1 — Drop first-party clients and their evidence

- Delete `ui/` (React SPA), `clients/rust` (GPUI), `task web:*`,
  `task desktop:*`, Playwright/GPUI e2e (`e2e/web_test.go`,
  `e2e/rust_desktop_test.go`), and every `coverage.json` web/desktop column.
- Keep `clients/ts` — it becomes the seed of the TS SDK in E4; keep
  `e2e/ts_smoke_test.go`.
- Rewrite iron rules 7/8 in `AGENTS.md`: client evidence = Go functional
  suite + SDK smoke tests. `verify:coverage` schema updated accordingly.
- Why first: removes the largest non-Go surface and the slowest CI legs, and
  nothing else depends on it.

### T1.2 — Drop flows

- Delete `flow.go`, `flowdef.go`, `flowdef_test.go`, `flowwork/`,
  `proto/ultra/v1/flow.proto`, `http/flow_handler.go`, `postgres/flow.go`,
  flow CLI verbs, flow e2e (`flow_*.go` — 4 files), `docs/flows.md`,
  `agent_docs/flows.md`.
- In `run.go`: remove `FlowInvocationID`, `FlowAgentName`; remove flow
  provenance from events and step classification.
- Loop `spawn.go`: remove flow-catalog spawning; `spawn_agent` /
  `run_agent_cohort` keep working from explicit per-call agent specs
  (prompt, model config, tools) — the orchestration tools survive, the
  versioned flow catalog does not.
- Consumer note recorded in the inventory: curri's Flow model (trigger +
  template + wiring) stays consumer-side; primer's tasks likewise.

### T1.3 — Drop billing and metering

- Delete `env_usage` machinery: metering columns/tables (handled fully by
  the E4 squash, but code first), watermark/`CloseAtWatermark`, rate class,
  `BillingService`/`GetUsage` from proto + http + testclient + devstack.
- Delete `Org.Plan` field and all readers (`postgres/org.go:18,32,100`).
- e2e: delete metering assertions from `env_test.go` / `phase7_test.go`
  (keep the lifecycle assertions those files also carry).

### T1.4 — Drop hosted isolation

- Delete `envprovider/k8s/hosted.go`, `hosted_test.go`, the `hosted_eks`
  provider kind, namespace/ServiceAccount/NetworkPolicy/ResourceQuota
  creation, `ULTRA_HOSTED_INGRESS_CIDRS`, and the ingress-CIDR refusal
  logic. `byo_k8s` remains the sole k8s adapter.
- Update `agent_docs/providers.md` kind table.
- Conformance suite: remove `namespace_isolation` / `resource_quota`
  capability rows and their conditional verifications; the capability
  *mechanism* (probe → store → explain) stays for E2.

### T1.5 — Drop presence and the human-multiplayer surface

- From `multiplayer.go`: delete presence types, participant roster,
  join/leave events for humans. Keep: session memory (scratchpad), run
  trees (parent/child), `ask_user` awaiting semantics — those move into
  `run.go` / `memory.go` as they are loop concerns, not multiplayer.
- Delete `loop/presence.go`, `http/multiplayer_handler.go` (fold surviving
  memory endpoints into `session_handler.go`), `e2e/multiplayer_test.go`
  (port its memory + run-tree assertions into `e2e/phase8_memory_test.go`
  and `e2e/agent_test.go` before deleting).
- `proto/ultra/v1/`: fold memory messages into session.proto; drop
  presence messages.

### T1.6 — Drop the grants lattice

- Delete `Grants.SubsetOf` lattice, `EnvAll`/`Envs` env authority,
  `MaySpawn`/`MaxChildren` monotone narrowing, `RootGrants`, cohort grant
  inheritance, `e2e/grants_test.go`, `e2e/phase8_grants_test.go`,
  `docs/security.md` lattice sections.
- **Interim safety:** replace with a flat per-run `Tools []string`
  allowlist checked at tool dispatch (`loop/step.go`). `"*"` = all.
  Spawned children default to the parent's allowlist verbatim (no
  narrowing semantics — E3 adds the consumer policy hook on top).
- Keep `CanonicalTools()` and the uniform-denial-stub behavior
  (`multiplayer.go:38-57` rationale): the existence-oracle defense is loop
  correctness, not lattice.
- Port the denial-visibility e2e assertions (denied tool → typed refusal
  event, no existence leak) into `e2e/agent_test.go`.

### T1.7 — Trim automation and CLI to match

- `automation.go` (periodic prompts) **stays** — primer's task automation
  needs it and it is 26 lines of generic substrate.
- `cmd/core` CLI: remove verbs for deleted surfaces; `task cli:test`
  updated.
- `cmd/devstack`: stack = pg + modelscript + cored + worker (web leg gone).

### T1.8 — Rename the module

- `github.com/aleksclark/ultracore` → `github.com/aleksclark/ultracore`
  (or final name — decide at execution, one commit).
- Binaries: `cored` → `cored`, `worker` → `coreworker`, `ultra` →
  `core`, env prefix `ULTRA_` → `CORE_`. Root package `ultra` → `core`.
- Proto package becomes `core.v1` **as a stub rename only** — the real
  surface reshape happens in E4; this item is mechanical `sed` + regen.
- Done last so the mass deletions above are reviewable without rename noise.

### T1.9 — Fences and docs

- Append E1 fence terms: `flowdef`, `FlowInvocation`, `BillingService`,
  `env_usage`, `hosted_eks`, `Presence`, `GPUI`, `Playwright`, `Org.Plan`,
  `RootGrants`, `SubsetOf`.
- Rewrite `AGENTS.md` cheatsheet + iron rules + docs index to the surviving
  surface. Delete `agent_docs/flows.md`, `multiplayer.md`; trim
  `providers.md`, `architecture.md`.

---

## Acceptance criteria

- **A1.1** `task build`, `task lint` (including fences), `task test`,
  `task test:functional`, `task cli:test`, `task verify:codegen`,
  `task verify:coverage` all green on the surviving suite.
- **A1.2** Fence proof: `git grep -inE` for every E1 fence term over
  `*.go|*.proto|*.sql|*.ts` (excluding docs/history) returns nothing.
- **A1.3** The loop still works end-to-end: an e2e run provisions a
  localdocker env, executes bash/edit through Bezalel, spawns a child agent
  with a narrowed tool allowlist, child's denied tool produces the uniform
  refusal event, `wait_for_agents` collects it, session replay from seq 0
  reproduces the full event stream. (This is largely existing
  `agent_test.go`/`env_test.go` coverage — the criterion is that it still
  passes after all deletions.)
- **A1.4** Session memory and run trees survived the multiplayer deletion:
  memory CRUD + caps and parent/child listing pass in e2e.
- **A1.5** Periodic prompts survived: existing automation e2e passes.
- **A1.6** All five surviving provider adapters (localdocker, byo_k8s,
  byo_nomad, static, tunnel) pass the conformance suite unmodified.
- **A1.7** Module renamed; `go build ./...` clean under the new path; no
  `ultralogical` import remains (fence term).
- **A1.8** Tree shrank: non-generated Go LOC recorded in the audit and
  compared against baseline (~30k). Expected: roughly one-third gone.
  `coverage.json` has no dangling test references (`verify:coverage`).

## Test coverage

| Behavior | Test | Tier |
|---|---|---|
| Loop e2e (provision → tool calls → spawn → wait → replay) | `e2e/agent_test.go`, `e2e/env_test.go` (surviving) | functional |
| Flat allowlist enforced at dispatch; uniform denial stub | new assertions in `e2e/agent_test.go` (ported from grants tests) | functional |
| Child inherits parent allowlist | new `loop` unit test + e2e assertion | unit + functional |
| Memory CRUD, caps, run trees post-move | `e2e/phase8_memory_test.go` (updated) | functional |
| Periodic prompts fire and enqueue runs | existing automation e2e | functional |
| Provider conformance ×5 kinds | `envprovider/conformance` | conformance |
| Orphaned-surface detection | fence script in `task lint` | CI |
| CLI verbs match surviving surface | `task cli:test` | CLI |

## Exit audit

`phase_e1_audit.md`: walks the inventory's drop/E1 rows one by one with the
deleting commit hash for each; confirms A1.1–A1.8 with command output; lists
any behavior intentionally weakened (grants → flat allowlist) with its E3
follow-up bullet. No open scoped bullet → E2.
