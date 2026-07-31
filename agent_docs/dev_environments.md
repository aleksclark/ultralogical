# Development environments

Phase 2 makes development environments session-owned durable resources.
Domain types/interfaces live in `env.go`; lifecycle orchestration in
`envwork/`; provider adapters in `envprovider/<dependency>/`; Bezalel MCP
transport in `mcp/`.

## Lifecycle

`requested → provisioning → ready → terminating → terminated`, with failures
to `failed`. Provision/terminate/restart/reconcile are typed River jobs.
Creation, EnvRequested, the provision job, and a reconcile watchdog are one
transaction. Reconcile is the only ready→failed writer and self-schedules while
ready.

**Interrupted provisioning.** Provisioning is two steps (create the resource,
then persist its handle), so a control-plane death between them could otherwise
duplicate resources. The handle is persisted before the readiness wait, and a
retry acquires the resource through `ultra.EnvAdopter` — `localdocker` finds the
container it already labelled — rather than creating a second one. The reconcile
watchdog re-drives a stalled requested/provisioning environment past the
provisioning deadline and fails it after ten deadlines, so recovery converges
without looping forever.

**Restart and rotation.** `EnvService.RestartEnv` rotates the environment's
bearer token, increments `epoch`, revokes cached tool clients, then replaces the
runtime while preserving the workspace volume. The prior token stops
authenticating and a client cached before the rotation fails locally with
`mcp.ErrRevoked`, so stale authority can never reach the environment. Lifecycle
events carry `epoch` so clients can distinguish a restarted environment from its
predecessor.

**Metering.** Every ready interval opens `env_usage`; reconciliation advances a
crash-safe watermark and terminal states close the interval. Recovery closes an
orphaned interval at its persisted heartbeat (`CloseAtWatermark`), so a dead
control plane under-counts by at most one heartbeat and can never over-count.
The ledger is org-scoped, append-only, closes once, and reports rate class
(`byo` today; hosted later).

## Providers

`ultra.EnvProvider` is the seam; every implementation runs
`envprovider/conformance`. `localdocker` launches the pinned real Bezalel
image with a named workspace volume, random localhost port, per-env bearer
token, and `ultralogical.env_id` label. Restart preserves the volume and
rotates the token; termination removes container + volume.

Two optional seams support durability and leak checks: `ultra.EnvAdopter` finds
a resource an interrupted provisioning already created, and
`ultra.EnvResourceLister` enumerates a provider's remaining resources so
conformance can prove termination released them.

The conformance suite is the bar for any new provider: provision and readiness,
health, authenticated tool discovery, `bash`, exact `edit` (including a failing
non-match), LSP, background jobs with retrievable output, caller-imposed
deadlines, wrong- and missing-token rejection, restart with workspace
persistence and token rotation, terminate, idempotent repeat terminate, resource
leak checks, and concurrent provisioning with distinct endpoints.

Tests build Bezalel from reviewed commit
`2504ff3152d0ee4e999210641d50ebf5483aa120` as
`ultralogical/bezalel:phase2-test`.

## MCP tools

`loop.EnvTools` resolves ready session environments on every agent step,
discovers Bezalel tools, and adapts them to `fantasy.AgentTool`. One ready
env exposes bare names (`bash`, `view`, ...); multiple envs are namespaced.
Native `provision_env`, `list_envs`, and `terminate_env` are always present.

Discovery and tool calls go through `mcp.Cache`, keyed by environment token
epoch: a restart yields a fresh client and revokes the previous one. Every tool
call carries a five-minute deadline, so a vanished or wedged environment
produces a typed error response instead of a hung step.

Tokens are random 32-byte values: SHA-256 hash + AES-GCM ciphertext in the
database, decrypted only at MCP use, and registered with the secret
redactor. `/health` is intentionally unauthenticated in Bezalel; `/mcp`
requires the token.

## API/UI

`EnvService` provides provision/get/list/terminate/restart/ExecPreview.
ExecPreview runs real Bezalel `bash` and appends a typed event so all subscribers
see the human action. `BillingService.GetUsage` exposes metered intervals with
their watermark and open/closed state. Provider instances are managed through
OrgService.

Both shipped clients drive the whole surface: the dark shadcn web application
renders an environment panel (lifecycle chips, epoch, restart, terminate,
ExecPreview) and a usage panel, and the dark GPUI desktop window renders the
same environment and usage state and exposes the same restart action.
