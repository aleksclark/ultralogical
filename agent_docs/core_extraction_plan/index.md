# Core Extraction Plan

> Extract the durable-session substrate out of Ultralogical-the-product and
> turn it into a minimal, multi-consumer **runtime + session persistence**
> service: event-sourced sessions, an owned agent loop, and pluggable
> per-tenant resource providers. Consumers (primer-server, curri-agents,
> future tools) bring their own UI, identity, triggers, and policy.

## What the core is

Three things, and only three things:

1. **Event-sourced sessions** — append-only, gapless per-session seq,
   subscribe-from-seq. Streaming, replay, multiplayer-of-services, and test
   assertions are all the same mechanism. Unchanged from today.
2. **The owned agent loop** — durable Fantasy loops, one step per queue job,
   transactional enqueue, versioned loop registry, spawn/wait/cohort
   orchestration, session memory. **Owned loop only.** The core never hosts,
   supervises, or adapts an alien agent runtime (no ACP, no A2A, no
   brain-in-the-pod). An MCP tool server *inside* a resource (Bezalel) is a
   tool surface, not a hosted agent, and stays.
3. **Session resources with pluggable providers** — the generalization of
   today's DevEnv. A session owns typed resources; a provider registration is
   **tenant-scoped** and supplies a lifecycle adapter + tool surface for its
   resource kind, proven by a shared conformance suite.

Plus the connective tissue those three require: structural tenancy
(`store.Tenant(id)`, today `store.Org(id)`), tenant credentials for BYO
inference, session labels for consumer-defined taxonomy, and a trimmed
ConnectRPC API with Go/TS SDKs.

## What the core is not

| Dropped | Why | Where it lives instead |
|---|---|---|
| Hosted agent runs (ACP/A2A bridging, supervisor sidecars) | Owned-loop-only decision. Removes protocol adaptation, stall heuristics, and the second message-history format | curri keeps Crush-in-pod on its legacy path until its `nativeagent` conversion lands on the core |
| Billing, metering (`env_usage`, BillingService), plans | Product, not substrate | future product layer on top, if ever |
| Hosted EKS isolation (namespaces, NetworkPolicy, ResourceQuota, ingress-CIDR refusal) | Exists to sell hosted compute; consumers here are trusted first-party services | dropped; `byo_k8s` covers real clusters |
| Flows (versioned catalog, flowdef language, invocation provenance) | Trigger + template + wiring is consumer vocabulary; curri already has its own Flow model | curri-agents; primer tasks |
| Multiplayer presence, human roster UX | Consumers own human identity and UI | consumer apps |
| Grants lattice (monotone privilege, cohort grants) | Over-general for first-party consumers | collapses to per-run tool allowlists + a consumer policy hook (E3) |
| First-party consumer web SPA + GPUI desktop clients | Consumers bring their own UI | `testkit/testclient` + SDK smoke tests become the client evidence; the private operator-only admin SPA in E5–E7 is not a consumer client |
| Human user/org-member model, interactive auth | Consumers are services | tenant API keys + opaque `Actor` attribution (E3); operator identity remains isolated to the private admin surface |

## Decision record

- **D1 — Extract in place.** Carve this worktree/branch down rather than
  greenfield a fourth implementation. The event log, loop, jobqueue, store
  seams, and provider conformance suite are battle-hardened by ten phases of
  audits; deletion is cheaper and safer than reimplementation.
- **D2 — Owned loop only.** See above. Anything whose only purpose was to
  observe or supervise an externally-driven agent is deleted, not deprecated.
- **D3 — Tenancy is structural and stays.** `store.Org(id)` scoping is kept
  verbatim and renamed `Tenant` so nobody re-attaches billing semantics.
  Missing and cross-tenant remain indistinguishable.
- **D4 — Providers are tenant-scoped registrations.** Adapters built per
  registration, probed at registration time, capabilities stored. This
  already exists (`envprovider/`, `capability.go`) and is kept, minus hosted.
- **D5 — Resources generalize environments.** `DevEnv` becomes one kind of
  `Resource`. The lifecycle machinery (adoption, reconcile watchdog, epoch /
  token rotation, conformance) is already generic in behavior; E2 makes it
  generic in name and type.
- **D6 — Consumers define taxonomy via labels.** Sessions carry opaque
  indexed key/value labels set by the consumer (`student=jacob`,
  `flow=pr-review`). The core never grows kind-specific columns.
- **D7 — Schema and proto reset to a v1 baseline.** There are no production
  deployments. Migrations squash to a fresh baseline; protos get one breaking
  reshape into `proto/core/v1`. After E4 closes, additive-only discipline
  resumes.
- **D8 — The module is renamed once** at the end of E1
  (`github.com/aleksclark/ultracore`, binaries `cored` / `coreworker` /
  `core` CLI), after the deletions, so the rename touches the minimum tree.
- **D9 — Administration is private and separate.** Debugging requires a
  comprehensive view across tenant boundaries, queue internals, provider
  state, raw event payloads, and runtime health. That power never enters
  `core.v1`: E5 adds a separately deployed `coreadmin` process and
  `admin.v1` API, E6 adds its private SPA, and E7 adds audited operations.
  Shared cursor pagination and typed server-side search/filtering are built
  before screens so no route fetches or renders unbounded collections.

## Phases

| Phase | Title | Depends on |
|---|---|---|
| [E0](phase_e0.md) | Baseline, inventory, and extraction fences | — |
| [E1](phase_e1.md) | Shed the product surface | E0 |
| [E2](phase_e2.md) | Generalize DevEnv → Resource + provider seam | E1 |
| [E3](phase_e3.md) | Tenancy, identity, labels, and policy hook | E2 |
| [E4](phase_e4.md) | Consumer surface: API v1, SDKs, ops hardening | E3 |
| [E5](phase_e5.md) | Private admin API and query foundations | E4 |
| [E6](phase_e6.md) | Comprehensive admin SPA | E5 |
| [E7](phase_e7.md) | Admin operations, audit, and production hardening | E6 |

## Iron rules of the extraction

These carry over from AGENTS.md, adapted:

1. **No mocks of our own components.** Real Postgres, real cored, real queue.
   The only fake is the scripted LLM server (`testkit/modelscript`).
2. **Tenancy is structural.** All tenant data access through
   `store.Tenant(id)`. Missing and cross-tenant indistinguishable.
3. **Deletion is complete or not done.** A phase that "drops" a feature
   removes its domain types, store methods, migrations (via the squash),
   protos, handlers, loop tools, tests, and docs. `grep -ri <term>` finding
   live code after the phase closes is a phase failure. Commented-out code
   and `_legacy` files are failures.
4. **The event log is the contract.** Per-session gapless seq; NOTIFY is a
   wakeup hint, never a data channel; assert against `Subscribe`.
5. **Seams stay clean.** No river/pgx types past `jobqueue`; handlers depend
   only on root interfaces; provider impls must pass the conformance suite
   unmodified.
6. **Coverage gate is redefined, not abandoned.** `e2e/coverage.json` maps
   every public capability to real Go functional tests plus (from E4) Go SDK
   and TS SDK exercises. Web/desktop entries are removed with their clients
   in E1. A capability without a coverage entry does not merge.
7. **Never claim unimplemented coverage.** Each phase keeps a
   behavior-to-test inventory (`phase_eN_inventory.md`) and closes only after
   an independent completion audit (`phase_eN_audit.md`) finds no open
   scoped bullet.
8. **Don't build ahead.** No speculative resource kinds, no plugin registry
   for hypothetical consumers, no config knob without a real caller. E5–E7
   may add operator visibility and controls but cannot change consumer
   semantics to make the admin implementation easier.
9. **Admin is a separate trust boundary.** `admin.v1`, `coreadmin`, and the
   admin SPA never share routes, auth credentials, generated clients, or
   deployment listeners with `core.v1`/`cored`. Every collection is bounded,
   cursor-paginated, and searched server-side; no admin screen may fetch an
   entire table.

## Testing tiers (post-extraction)

| Tier | Command | Requires |
|---|---|---|
| Unit + store + queue + provider conformance | `task test` | docker |
| Functional acceptance (`e2e/`) | `task test:functional` | real stack |
| CLI | `task cli:test` | real stack |
| SDK smoke (Go + TS) | `task sdk:test` (new, E4) | real stack |
| Admin API functional + query conformance | `task admin:test` (E5) | real Postgres + coreadmin |
| Admin SPA golden + performance | `task admin:web:test` (E6) | real admin stack + seeded large dataset |
| Admin security/operations | `task admin:security:test` (E7) | private-route harness + real stack |
