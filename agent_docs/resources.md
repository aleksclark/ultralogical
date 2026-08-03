# Session resources

A session owns durable **Resources**. Each resource has a kind (today
`dev_env`, plus the test-only `null_resource`), a lifecycle state machine, an
opaque provider handle, and optionally an authenticated tool endpoint.

## Lifecycle

```
requested → provisioning → ready ⇄ suspended → terminating → terminated
                         ↘ failed
```

- **requested / provisioning**: a provision job (and watchdog reconcile) is
  armed. Interrupted provisioning adopts via `ResourceAdopter` when the
  provider can find work it already created.
- **ready**: tools may be served when `serves_tool_endpoint` is claimed and
  `Endpoint` is non-empty.
- **suspended**: host temporarily unreachable; durable state still exists
  (tunnel). Resume is ready, not a new provision.
- **terminating / terminated / failed**: terminal. Reconcile is the only
  ready→failed writer.

Job kinds: `resource.provision`, `resource.terminate`, `resource.restart`,
`resource.reconcile`.

## Provider seam

```go
type ResourceProvider interface {
    Kind() ResourceKind
    ValidateSpec(spec json.RawMessage) error
    Provision(ctx, r Resource, token string) (handle json.RawMessage, err error)
    Status(ctx, r Resource) (ResourceStatus, error)
    Endpoint(ctx, r Resource) (ToolEndpoint, error)
    Restart(ctx, r Resource, token string) (handle json.RawMessage, err error)
    Terminate(ctx, r Resource) error
    HealthCheck(ctx, r Resource) error
}
```

Optional: `ResourceAdopter`, `ResourceLister` (`ListOwned`), `CapabilityProber`.

Handles are durable JSON `{"version":1,"data":...}` via `provider/handlefmt`.

## Capabilities

Optional capabilities change *how* conformance verifies behavior, never
*whether* core lifecycle is verified:

- `restart_preserves_state`
- `tolerates_disconnect`
- `adopts_orphans`
- `enumerates_resources`
- `serves_tool_endpoint`

Core contract: Provision, Health, TokenRejection, RestartRotatesToken,
Terminate, LeakCheck, ConcurrentProvisionDistinctEndpoints.

Tool-surface contract (kinds with tool endpoints): Discovery, Bash, ExactEdit,
LSP, BackgroundJobAndTimeout, PerCallDeadline.

## API and tools

RPCs: `ProvisionResource`, `GetResource`, `ListResources`, `TerminateResource`,
`RestartResource`, `ExecPreview`.

Native loop tools: `provision_resource`, `list_resources`, `terminate_resource`.
Aliases `provision_env` / `list_envs` / `terminate_env` remain grantable.

Events: `resource_requested` … `resource_terminated` / `resource_suspended`
carry `resource_id`, `kind`, `name`, `epoch`.

## Kinds

| Resource kind | Provider adapter kinds | Tool endpoint |
|---|---|---|
| `dev_env` | `local_docker`, `byo_k8s`, `byo_nomad`, `tunnel_local`, `static` | Bezalel MCP |
| `null_resource` (test-only) | `null` | none |

`Resource.Kind` is the resource kind. `ProviderInstance.Kind` is the adapter
kind. `ResourceProvider.Kind()` returns the resource kind the adapter hosts.

## Store and schema

`ResourceStore` is org-scoped via `store.Org(id).Resources()`. Spec and handle
are `json.RawMessage`. The interim table remains `dev_envs` with an additive
`kind` column (`postgres/migrations/00010_resources.sql`); E4 squashes the
schema. Empty handle detection uses `HandlePresent`.

## Loop integration

`loop.ResourceTools` resolves ready session resources each step, collects
MCP tools for kinds that publish endpoints (via the resource's tool endpoint /
MCP client — the practical ToolSurface), and namespaces when more than one
ready resource of the same kind publishes tools (`dev_env:<name>/…`).
`mcp.Cache` is keyed by `(resource_id, epoch)` so restart invalidates clients.

## Conformance

`provider/conformance` splits into core (every kind) and tool-surface
(`dev_env`). Real adapters run both; `null_resource` runs core only via
`SkipToolSurface`.
