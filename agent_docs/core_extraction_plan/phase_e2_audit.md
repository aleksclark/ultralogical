# Phase E2 audit

Exit audit for Phase E2 (DevEnv → Resource rename-and-reseam). Working tree
left uncommitted on branch `core-extraction` (E1 base `164831f`).

## Attestations

### A2.1 — Full suite green

| Command | Result |
|---------|--------|
| `go build ./...` | PASS |
| `bash scripts/check-extraction-fences.sh` | PASS (e1+e2 clean) |
| `go vet ./...` | PASS |
| `buf lint` | PASS |
| `go test $(go list ./... \| grep -v '/e2e$')` | PASS (all packages; k8s after kind image tag present) |
| `go test ./provider/k8s/` | PASS (conformance + adoption + reconcile) |
| `go test ./e2e/ -count=1` | PASS (~156s) |
| `go test ./cmd/core/...` | PASS |
| `python3 scripts/verify-coverage.py` | PASS (35/35 RPCs) |
| `bash scripts/verify-codegen.sh` | PASS |

`golangci-lint` was not installed in this environment; CI runs it. `buf lint`
and `go vet` ran clean.

### A2.2 — Behavior-preservation (e2e mechanical only)

Surviving E1 e2e tests were updated with mechanical renames only:

- event kind strings `env_*` → `resource_*`
- generated clients `EnvService` → `ResourceService`, `ProvisionEnv` →
  `ProvisionResource`, `EnvState` → `ResourceState`, etc.
- store `Envs()` → `Resources()`, `EnvID` → `ResourceID`
- import paths `envprovider` → `provider`, `envwork` → `resourcework`

No intentional semantic weakening of assertions for the `dev_env` path. New
coverage is additive in `e2e/resource_kinds_test.go`.

### A2.3 — Five real providers pass core + tool-surface

| Provider | Core | Tool surface |
|----------|------|--------------|
| localdocker | PASS | PASS |
| byo_k8s | PASS | PASS |
| byo_nomad | PASS | PASS |
| tunnel_local | PASS | PASS |
| static | PASS | PASS |

### A2.4 — null_resource core + mixed-kind e2e

- `provider/nullresource` passes `conformance.RunWith` with `SkipToolSurface`
  and capabilities `adopts_orphans`, `enumerates_resources` (not
  `serves_tool_endpoint`).
- `TestResourceKinds_DevEnvAndNullConcurrent` provisions both kinds
  concurrently, asserts ready, kind-tagged interleaved lifecycle events,
  RestartResource on null, list of both kinds, loop `provision_resource` of a
  second null, API terminate of all three, docker ListOwned leak check for
  dev_env, and store ListActive empty for the session (null release).
  PASS (~8s in isolation after review fixes).

### A2.5 — Lifecycle-only kind via API + loop tools

null_resource is manageable without a tool endpoint: provision/list/terminate/
restart via ResourceService; loop native tools `list_resources` and
`provision_resource` for null_resource (tool_result assertions); terminate/
restart via API in the same e2e. Env aliases retained. Empty endpoint
asserted. `awaitHealthy` skips `mcp.Healthy` when endpoint is empty or
`!CapabilityServesToolEndpoint`.

### A2.6 — Adoption + watchdog survive rename

`testkit/resourceconverge` (ex-envconverge) drives provision/terminate/
restart/reconcile against real Postgres + inproc queue. k8s reconcile tests
`TestA102_KubernetesAdoptsInterruptedProvisioning` and
`TestA102_KubernetesReconcilesExternallyDeletedPod` PASS.

### A2.7 — Grep fence clean

`fences/e2.txt` bans:

```
\bDevEnv\b
EnvProvider
EnvStore
\benvwork\b
EnvRequested
EventKindEnv
\benvprovider\b
```

`provision_env` deliberately **not** fenced (alias retained through E5/E6).
`DevEnvSpec` remains allowed (kind schema name). `check-extraction-fences.sh`
reports clean.

### A2.8 — resources.md matches code

`agent_docs/resources.md` documents Resource lifecycle, ResourceProvider
seam (Status/Endpoint/HealthCheck retained), handle wire format, capability
names including `restart_preserves_state`, core vs tool-surface contracts,
API RPCs, loop tools + aliases, event vocabulary, and kind-scoped MCP tool
namespacing (`dev_env:<name>/…`). AGENTS.md index points at `resources.md`.
`providers.md` updated for the kind-parameterized model.

## Conformance mapping (old → new)

| Old subtest | Core | Tool surface | Notes |
|-------------|------|--------------|-------|
| Provision | yes | — | endpoint required only if `serves_tool_endpoint` |
| ProviderNativeResources | yes | — | Inspect |
| Health | yes | — | HealthCheck/Status; `mcp.Healthy` if endpoint |
| Discovery | — | yes | unchanged strength for dev_env |
| Bash | — | yes | unchanged |
| ExactEdit | — | yes | unchanged (including non-match failure) |
| LSP | — | yes | unchanged |
| BackgroundJobAndTimeout | — | yes | unchanged |
| PerCallDeadline | — | yes | unchanged |
| TokenRejection | core\* | — | skipped for lifecycle-only kinds |
| RestartRotatesToken | yes | — | workspace/state check if `restart_preserves_state` |
| Terminate (+ idempotent) | yes | — | unchanged |
| LeakCheck | yes | — | via `ListOwned` filtered by id (was `Resources(envID)`) |
| ConcurrentProvisionDistinctEndpoints | yes | — | endpoints if tool surface; else distinct handles/ids |

No existing dev_env check was weakened: `RunWith` for real providers still runs
core + tool-surface fully.

## Fence terms active

See `fences/e2.txt`. Enforced by `scripts/check-extraction-fences.sh` via
`task lint`.

## Behavior risks / notes

1. Error strings now say "resource unavailable" in places that previously said
   "environment unavailable" — e2e updated mechanically where asserted.
2. Docker/k8s/nomad label key is `ultracore.resource_id` (was
   `ultracore.env_id`). No prod data on this branch.
3. Handle **envelope** remains `{"version":1,"data":...}`; some handle **data**
   fields renamed `env_id` → `resource_id` inside provider-owned JSON.
4. Table stays `dev_envs` with additive `kind` column until E4 squash.
5. `provision_env` / `list_envs` / `terminate_env` aliases retained (not fenced).

## Review corrections (post-implementation adversarial pass)

Gaps found and fixed without commit:

1. **static handle field** `EnvID` → `ResourceID` (JSON key was already
   `resource_id`).
2. **k8s reconcile test** helper `sanitizedEnvID` → `sanitizedResourceID`.
3. **Loop MCP namespacing** now uses `kind:name/` and only when >1 ready
   resource of the *same* kind (matches plan T2.4 / resources.md); previously
   hard-coded `env:` for any multi-ready set.
4. **Kind mismatch rejection** in `resourcework.Request`: caller kind must
   match `provider.Kind()` when both are set (prevents silent kind rewrite).
5. **null provider wiring** shares one in-memory instance per process so
   adopt/list stay coherent across job-scoped `Build` calls.
6. **A2.5 e2e strengthened**: asserts `list_resources` tool_result contains
   both ids; loop `provision_resource` of a second null_resource with success
   tool_result; dual null terminate + leak coverage.
7. **resources.md** loop section aligned with ToolSurface-via-MCP reality and
   kind-scoped prefix.

Deferred (explicitly OK per plan): `provision_env`/`list_envs`/`terminate_env`
aliases and terminate input `env_id`; table name `dev_envs` until E4; k8s
label value `ultracore.dev/env-id` (stable cluster label, not Go identifier);
`DevEnvSpec` kind schema name.
