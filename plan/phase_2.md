# Phase 2 — Dev envs: local provider + env tools in the loop

**Duration:** 2–3 weeks · **Depends on:** Phase 1

## Goal

Dev environments become a core, session-owned abstraction. An agent (or human) can
provision an environment, the loop gains real tools (bash, file editing, search, LSP)
via bezalel over MCP-HTTP, envs outlive any individual run or worker, and everything is
visible in the UI. The provider seam and its conformance suite are established here so
nomad/k8s (Phase 5) are additive.

## Scope

**In:**
- `envprovider` package: `Provider` interface, `EnvSpec`, `EnvStatus`, `Handle`.
- **Provider instances**: `provider_instances` table (org-scoped registrations of
  where envs run: kind + config + optional credential ref);
  `OrgService.{RegisterProvider,ListProviders,DeleteProvider}`; envs reference an
  instance, not a bare provider name.
- **local_docker provider**: docker containers running the bezalel image (dev/CI
  instance kind; hosted tenants get real kinds in Phase 5).
- **Usage metering ledger**: `env_usage` interval opened at `EnvReady`, closed at
  terminal state, heartbeat-ticked by the reconciler so crashes can't leave unbounded
  open intervals; tagged with the instance's rate class.
- `dev_envs` table, env state machine, per-env bearer tokens (SHA-256 hash at rest +
  encrypted cleartext for worker use).
- Env lifecycle jobs: `env.provision`, `env.terminate`, `env.reconcile` (periodic).
- `EnvService`: ProvisionEnv, GetEnv, ListEnvs, TerminateEnv, ExecPreview.
- `mcpTool` adapter: discovers bezalel tools via `tools/list`, wraps each as a
  `fantasy.AgentTool`, namespaced per env.
- Native tools: `provision_env`, `list_envs`, `terminate_env`.
- Event variants: `EnvRequested`, `EnvProvisioning`, `EnvReady`, `EnvFailed`,
  `EnvTerminating`, `EnvTerminated`, `ExecPreviewRan`.
- Provider conformance suite.
- UI: env panel (state timeline, spec), richer tool-call cards (bash stdout, file
  diffs), ExecPreview command box.

**Out:** nomad/k8s/hosted-EKS/tunnel providers (Phase 5), billing on the ledger
(Phase 12 — this phase only records usage), switchboard attachment (Phase 6),
suspend/resume (post-v1; state machine reserves the states).

## Design details

### Env state machine & lifecycle jobs

```
requested → provisioning → ready ⇄ suspended → terminating → terminated
                 └──────────→ failed ←──────────────┘
```

- `ProvisionEnv` (RPC or native tool): tx-inserts the `dev_envs` row (`requested`),
  appends `EnvRequested`, and `EnqueueTx`s `env.provision` — the same
  atomic-creation pattern as runs (no orphaned rows).
- `env.provision` job: calls `provider.Provision(spec)` → stores handle → polls
  `Status` until ready/deadline → verifies bezalel `GET /health` with the env token →
  state `ready` + `EnvReady{endpoint}` event. Any failure → `failed` + `EnvFailed`
  with a structured, human-readable reason.
- `env.reconcile` (periodic, queue-scheduled): for each non-terminal env, compare
  desired vs. `provider.Status` + `/health`; dead envs → `failed` + event; terminated
  handles cleaned up. Reconciliation is the *only* writer allowed to move ready→failed,
  avoiding dual write paths.
- `env.terminate` job: provider.Terminate, verify gone, state `terminated`.

### Provider instances & metering

- `ProvisionEnv`/`provision_env` take `provider_instance` (name, resolved within the
  org; defaulting per org config). Instance kinds are validated against the deployment
  whitelist. Instance health (`last_healthy_at`) is maintained by the reconciler.
- Metering: the `EnvReady` transition inserts an `env_usage` row (`started_at`); every
  reconcile tick advances a `last_metered_at` watermark; terminal transitions close the
  interval. A crashed control plane therefore under-counts by at most one tick — never
  over-counts. Rate class comes from the instance kind (`local_docker`/BYO → `byo`,
  hosted kinds → `hosted`). The ledger is append-only; corrections are compensating
  rows.

### Local provider

- Runs `ghcr.io/aleksclark/bezalel` (pinned digest) with `--workdir /work
  --auth-token $TOKEN`, a named docker volume for `/work` (env identity = volume, so a
  container restart preserves the workspace), label-tagged
  (`ultralogical.env_id=<id>`) for reconcile/GC.
- `Endpoint` returns the mapped host port. CI-friendly: no privileged mode, no
  docker-in-docker requirement beyond what testcontainers already needs.

### Tokens

- Minted per env at provision: 32-byte random, `token_hash = sha256`, `token_enc =
  AES-GCM(cleartext, master key from env/KMS)`. Workers decrypt to call bezalel;
  clients never see it (ExecPreview goes through ultrad).
- Rotation on env restart: new token, old hash invalidated.

### mcpTool adapter (`loop/mcptool`)

- On step start, for each env attached to the run: MCP `initialize` + `tools/list`
  (cached per env epoch, invalidated on env restart), wrap each tool as a non-generic
  `fantasy.AgentTool` whose `Run` forwards `ToolCall.Input` verbatim to `tools/call`
  and maps MCP `isError` → `ToolResponse.IsError`.
- Naming: single-env runs expose bare names (`bash`, `edit`); multi-env runs prefix
  (`env:main/bash`). The run's tool grants (from its token) filter what's exposed.
- Timeouts: per-tool-call deadline (default 5 min, matching bezalel's long-command
  window); env unreachable → structured error ToolResponse (never a hang), and a
  reconcile is nudged.

### ExecPreview

Humans run one-off commands in an env from the UI: ultrad forwards to bezalel `bash`,
appends `ExecPreviewRan{command, output, exit_code, actor}` — so human actions live in
the same history agents see (agents can read the session log via `post_event`-adjacent
context in later phases). This is the "visible and controllable by people" principle
made concrete.

### Provider conformance suite (`envprovider/conformance`)

Black-box suite parameterized over a `Provider` factory:

1. Provision a minimal spec → status reaches `ready` within deadline.
2. `GET /health` on endpoint returns 200 with the minted token; 401 without.
3. MCP `tools/call bash "echo hi"` → stdout `hi`, exit 0.
4. `write` a file, `view` it back, `edit` it, verify content.
5. Workspace persistence: restart the env (provider-specific), file still present,
   old token rejected, new token accepted.
6. Terminate → status `terminated`; underlying resource verifiably gone; double
   terminate is idempotent.
7. Concurrent provision of 3 envs → all ready, endpoints distinct.

## Work breakdown

1. Proto additions (EnvService, provider-instance RPCs, `BillingService.GetUsage`,
   event variants) + codegen.
2. Migrations + store for dev_envs, provider_instances, env_usage; token
   mint/hash/encrypt helpers.
3. envprovider interface + conformance suite skeleton.
4. Local provider + conformance green.
5. Lifecycle jobs (provision/terminate/reconcile) + events.
6. mcpTool adapter + tool-grant filtering + step-job wiring.
7. Native tools (`provision_env`, `list_envs`, `terminate_env`).
8. ExecPreview end-to-end.
9. UI: env panel, tool cards, ExecPreview box, org provider-instance settings page,
   usage display (env-hours this period).
10. Functional tests A2.2–A2.5, A2.7, Playwright A2.6.

## Acceptance tests

- **A2.1 — Provider conformance (local).** The full conformance suite passes against
  the local provider in CI.
- **A2.2 — Agent does real work.** Scripted run: agent calls `provision_env`, waits for
  ready (the tool blocks with progress events), runs `bash: git init && echo hi >
  README.md`, `view README.md`, completes. Assert: `ToolResult` events carry the real
  git/stdout output; harness independently `docker exec`s into the container mid-run
  and verifies `README.md` exists (proving no simulation layer).
- **A2.3 — Env outlives loops and workers.** Run 1 writes `state.txt`, completes. Run 2
  (same session, same env) reads it back — content matches. Then: a run with two
  env-using steps is SIGKILLed between them (A1.2 pattern); after worker restart the
  env is untouched (same container ID) and the resumed step reads data written by the
  pre-crash step.
- **A2.4 — Env auth.** Direct MCP call to the env endpoint with a wrong bearer → 401.
  DB contains only hash + ciphertext (test greps the row for the cleartext and fails if
  found). After env restart, the old token 401s and the rotated token works; the tool
  cache epoch invalidates.
- **A2.5 — Reconciler catches death.** Harness `docker kill`s the container. Within one
  reconcile interval: env state `failed`, `EnvFailed` event with a structured reason.
  An agent tool call against the dead env during the window returns a structured error
  ToolResponse within its deadline (no hang), and the run can continue (script handles
  the error).
- **A2.6 — Playwright golden.** Browser: provision an env from the UI and watch state
  chips progress `requested → provisioning → ready` live; start a run that uses bash;
  expand the tool card and see real stdout; type a command in ExecPreview and see the
  result; open the session in a second browser context and see the ExecPreview event
  appear there too.
- **A2.7 — Metering correctness.** (a) Provision → wait ~10s → terminate: exactly one
  `env_usage` interval whose duration matches ready→terminated wall time within one
  reconcile tick. (b) `docker kill` the env (A2.5 path): the interval closes at the
  failure detection, not later. (c) SIGKILL the worker mid-life: after restart +
  reconcile, no interval remains open beyond the watermark. (d) Intervals carry the
  correct org, instance, and rate class; org B cannot read org A's usage via
  `BillingService.GetUsage`.

## Exit criteria

- A2.1–A2.7 green in CI. Conformance suite documented as the bar for new providers.
- Reconciler runs in `task dev` stack; killing a dev container self-heals state.
- Functional suite < 5 min (env tests dominate; parallelize by session).
