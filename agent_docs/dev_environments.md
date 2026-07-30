# Development environments

Phase 2 makes development environments session-owned durable resources.
Domain types/interfaces live in `env.go`; lifecycle orchestration in
`envwork/`; provider adapters in `envprovider/<dependency>/`; Bezalel MCP
transport in `mcp/`.

## Lifecycle

`requested → provisioning → ready → terminating → terminated`, with failures
to `failed`. Provision/terminate/reconcile are typed River jobs. Creation,
EnvRequested, and the provision job are one transaction. Reconcile is the
only ready→failed writer and self-schedules while ready.

Every ready interval opens `env_usage`; reconciliation advances a crash-safe
watermark and terminal states close the interval. The ledger is org-scoped,
append-only, and reports rate class (`byo` today; hosted later).

## Providers

`ultra.EnvProvider` is the seam; every implementation runs
`envprovider/conformance`. `localdocker` launches the pinned real Bezalel
image with a named workspace volume, random localhost port, per-env bearer
token, and `ultralogical.env_id` label. Restart preserves the volume and
rotates the token; termination removes container + volume.

Tests build Bezalel from reviewed commit
`2504ff3152d0ee4e999210641d50ebf5483aa120` as
`ultralogical/bezalel:phase2-test`.

## MCP tools

`loop.EnvTools` resolves ready session environments on every agent step,
discovers Bezalel tools, and adapts them to `fantasy.AgentTool`. One ready
env exposes bare names (`bash`, `view`, ...); multiple envs are namespaced.
Native `provision_env`, `list_envs`, and `terminate_env` are always present.

Tokens are random 32-byte values: SHA-256 hash + AES-GCM ciphertext in the
database, decrypted only at MCP use, and registered with the secret
redactor. `/health` is intentionally unauthenticated in Bezalel; `/mcp`
requires the token.

## API/UI

`EnvService` provides provision/get/list/terminate/ExecPreview. ExecPreview
runs real Bezalel `bash` and appends a typed event so all subscribers see the
human action. `BillingService.GetUsage` exposes metered intervals. Provider
instances are managed through OrgService.
