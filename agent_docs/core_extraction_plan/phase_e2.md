# Phase E2 — Generalize DevEnv → Resource + provider seam

**Objective:** Turn the environment subsystem into the generic
session-resource subsystem: a session owns typed **Resources**; a
tenant-scoped **provider registration** supplies the lifecycle adapter and
tool surface for its resource kind. The existing env machinery becomes the
first (and initially only) resource kind, `dev_env`, with zero behavior
change.

**Depends on:** E1.
**Duration guess:** 2 weeks.

---

## Scope

Rename-and-reseam, not rewrite. The lifecycle state machine, adoption,
reconcile watchdog, epoch/token rotation, MCP cache, and conformance suite
are already the right behavior (see `agent_docs/dev_environments.md`); this
phase makes their types kind-agnostic and moves env-specific vocabulary
behind the provider.

**Explicit YAGNI:** no second resource kind is built in this phase. The
seam's generality is proven by the conformance suite + a test-only toy kind,
not by speculatively shipping browsers/VMs.

## Target seam

```go
// core (root package)

type ResourceID string
type ResourceKind string          // "dev_env" today; provider-declared

// Resource replaces DevEnv. Lifecycle unchanged:
// requested → provisioning → ready ⇄ suspended → terminating → terminated | failed
type Resource struct {
    ID        ResourceID
    SessionID SessionID
    TenantID  TenantID            // OrgID until E3 renames it
    Kind      ResourceKind
    ProviderInstanceID ProviderInstanceID
    Spec      json.RawMessage     // kind-specific, provider-validated
    Handle    json.RawMessage     // kind-specific, provider-owned (was: container id / pod name…)
    Endpoint  ToolEndpoint        // zero-valued when !cap.serves_tool_endpoint
    Epoch     int
    State     ResourceState
    // …timestamps, failure reason
}

// ResourceProvider generalizes EnvProvider. One registration = one adapter.
type ResourceProvider interface {
    Kind() ResourceKind
    ValidateSpec(spec json.RawMessage) error
    Provision(ctx context.Context, r Resource) (handle json.RawMessage, ep ToolEndpoint, err error)
    Terminate(ctx context.Context, r Resource) error
    Restart(ctx context.Context, r Resource) (ToolEndpoint, error)  // preserves workspace/state, rotates token
    HealthCheck(ctx context.Context, r Resource) error
}

// Optional seams, unchanged in spirit from EnvAdopter / EnvResourceLister:
type ResourceAdopter interface { Adopt(ctx context.Context, r Resource) (json.RawMessage, ToolEndpoint, bool, error) }
type ResourceLister  interface { ListOwned(ctx context.Context) ([]OwnedResource, error) }

// ToolSurface: how a ready resource contributes tools to the loop.
// dev_env answers with its Bezalel MCP endpoint; a kind with no tools
// returns nothing and the resource is lifecycle-only.
type ToolSurface interface {
    Tools(ctx context.Context, r Resource) ([]fantasy.AgentTool, error)
}
```

Provider **registrations** stay exactly as they are (tenant-scoped, adapter
built per registration, probed read-only at registration, capabilities
stored — `envprovider/registry.go`, `capability.go`), with `Kind` added to
the registration record.

## Work items

### T2.1 — Root types and store

- `env.go` → `resource.go`: types above. `EnvID` → `ResourceID` etc.
  Add `Kind` column; `Spec`/`Handle` become raw JSON owned by the provider
  (today's typed image/resources/workdir spec becomes the `dev_env` spec
  schema, validated in the provider's `ValidateSpec`).
- `store.go`: `EnvStore` → `ResourceStore`, scoped queries gain `kind`
  filters. Postgres impl renamed; schema change lands via the E4 squash,
  interim migration is additive.
- Events: `EnvRequested/EnvReady/…` → `ResourceRequested/…` carrying
  `kind`. **Event vocabulary is part of the contract** — update
  `event.go`, proto event types, and every subscriber assertion.

### T2.2 — Lifecycle work (`envwork/` → `resourcework/`)

- Same jobs (provision / terminate / restart / reconcile), same
  transactional creation (create + Requested event + provision job +
  watchdog in one tx), same adoption-on-retry and ten-deadline failure
  policy, same epoch/token rotation. Only the types and job arg names
  change.
- Reconcile stays the only ready→failed writer and still self-schedules.

### T2.3 — Provider tree (`envprovider/` → `provider/`)

- `localdocker`, `k8s`, `nomad`, `static`, `tunnel` implement
  `ResourceProvider` with `Kind() == "dev_env"`.
- Capability probing unchanged; capability names de-env'd where needed
  (`restart_preserves_workspace` → `restart_preserves_state`, others keep).
- Conformance suite (`provider/conformance`) re-typed. Its contract is
  unchanged: provision/readiness, health, authenticated tool discovery,
  bash + exact edit, background jobs, deadlines, token rejection ×2,
  restart persistence + rotation, terminate, idempotent re-terminate, leak
  check via lister, adoption, concurrent provisioning with distinct
  endpoints. Where a check is `dev_env`-specific (bash/edit/LSP), the suite
  splits into **core contract** (lifecycle, auth, leaks, adoption — every
  kind) and **tool-surface contract** (driven by the kind's declared tool
  schema; `dev_env` supplies the bash/edit/LSP cases).

### T2.4 — Loop integration

- `loop/envtools.go` → `loop/resourcetools.go`: resolve ready session
  resources each step, collect `ToolSurface.Tools()`, namespace when >1
  resource of a kind. Native tools become `provision_resource(kind, spec)`,
  `list_resources`, `terminate_resource` (aliases `provision_env` etc.
  kept through E5/E6 migrations, then dropped — record as a fence term for
  a post-E6 cleanup).
- `mcp.Cache` keyed by (resource, epoch) — rename only.

### T2.5 — Toy kind proves the seam

- Test-only `provider/fake` registering kind `null_resource`: no tool
  endpoint, trivial lifecycle, in-memory lister/adopter. It must pass the
  **core contract** conformance suite unmodified.
- e2e: a session provisions one `dev_env` and one `null_resource`
  concurrently; both reach ready; lifecycle events for both kinds
  interleave correctly in the log; terminating the session releases both
  (leak check on both providers).

### T2.6 — API + CLI + docs

- `EnvService` → `ResourceService` (proto stub-level rename; full reshape
  in E4). Handlers, testclient, devstack, CLI verbs renamed.
- `agent_docs/dev_environments.md` → `resources.md`;
  `providers.md` updated to the kind-parameterized model.
- Fence terms appended: `DevEnv`, `EnvProvider`, `EnvStore`, `envwork`,
  `EnvRequested` (etc. event names), `provision_env` *(deferred until the
  alias drop)*.

---

## Acceptance criteria

- **A2.1** Full suite green (`task build/lint/test/test:functional/cli:test`,
  codegen + coverage verifies).
- **A2.2** Behavior-preservation: every surviving E1 e2e test passes with
  only mechanical renames in its assertions (no semantic edits). The audit
  diff-reviews e2e changes to enforce this.
- **A2.3** All five real providers pass the re-typed conformance suite
  unmodified (core + tool-surface contracts).
- **A2.4** `null_resource` passes the core contract; the mixed-kind e2e
  (T2.5) passes, including interleaved event ordering and dual leak checks.
- **A2.5** A resource kind with no tool endpoint is fully manageable:
  provision/list/terminate/restart via API and via loop native tools, with
  correct events — proving `serves_tool_endpoint=false` paths.
- **A2.6** Interrupted-provisioning adoption and reconcile-watchdog behavior
  demonstrably survive the rename: the existing convergence tests
  (`testkit/envconverge` → `resourceconverge`) pass for both kinds.
- **A2.7** Grep fence: no `DevEnv`/`EnvProvider`/`envwork` identifiers in
  live code.
- **A2.8** `resources.md` documents the seam exactly as implemented
  (audited line-by-line against the code, per repo convention that guides
  are executable/verified).

## Test coverage

| Behavior | Test | Tier |
|---|---|---|
| Core lifecycle contract per provider ×6 (5 real + fake) | `provider/conformance` | conformance |
| Tool-surface contract (`dev_env`) ×5 real providers | `provider/conformance` | conformance |
| Mixed-kind session e2e | new `e2e/resource_kinds_test.go` | functional |
| Lifecycle-only kind via API + loop tools | new assertions, same file | functional |
| Adoption + watchdog convergence both kinds | `testkit/resourceconverge` | store/queue |
| Loop tool namespacing with multiple resources | existing env e2e (renamed) | functional |
| Epoch rotation revokes cached clients | `mcp/cache_test.go` (renamed) | unit |
| Event vocabulary carries `kind` | eventbus store tests + e2e replay assertions | store + functional |

## Exit audit

`phase_e2_audit.md`: confirms A2.1–A2.8; specifically attests (a) no
semantic e2e edits beyond renames, (b) conformance suite split didn't weaken
any existing check (old→new check mapping table), (c) fence terms active.
