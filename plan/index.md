# Ultralogical — Implementation Plan

> The missing layer is a durable session around the work itself: one that can span
> applications and environments, provide relevant context by default, expose structured
> data and actions, preserve history, and be driven by software while remaining visible
> and controllable by people.

Ultralogical is a durable-session platform for agentic work. A **Session** is the unit of
work — not a chat, not a pod, not a terminal. It outlives any single process, UI, agent
loop, or environment. Humans and agents join it, act in it, observe it, and leave it, and
the session remains: its history, its environments, its running loops, its structured
state.

Ultralogical is **multi-tenant from day 1** and built to run as a paid hosted service:
organizations bring their own inference credentials (OpenAI / Anthropic / Bedrock), pay
per user per month plus metered dev-env usage time, and choose where their dev envs run —
our hosted EKS (the upsell), their own k8s or nomad clusters (BYO creds), or their own
machines via a cloudflared-connected local provider.

## Plan structure

| Phase | Title | Duration | Depends on |
|---|---|---|---|
| [Phase 0](phase_0.md) | Skeleton & contracts | 1–2 weeks | — |
| [Phase 1](phase_1.md) | Durable agent loop + minimal web UI | 2–3 weeks | 0 |
| [Phase 2](phase_2.md) | Dev envs: local provider + env tools | 2–3 weeks | 1 |
| [Phase 3](phase_3.md) | Multiplayer, multi-loop, agent-spawns-agent | 2 weeks | 2 |
| [Phase 4](phase_4.md) | Flows | 1–2 weeks | 3 |
| [Phase 5](phase_5.md) | BYO & hosted providers: nomad, k8s, cloudflared local, hosted EKS | 2–3 weeks | 2 (parallel with 4) |
| [Phase 6](phase_6.md) | Advanced loop & tool ergonomics | 2 weeks | 4 |
| [Phase 7](phase_7.md) | Production hardening, billing & queue-swap proof | 2–3 weeks | 6 |
| [Phase 8](phase_8.md) | Rust/gpui UI | 3–4 weeks | 4 (parallelizable) |

Every phase ships a **vertical slice**: backend + client + UI + tests. A phase is done
when its acceptance tests pass in CI. No phase builds backend surface that isn't
exercised end-to-end within that phase.

---

## 1. Synthesis of learnings

| Source | What we take | What we deliberately avoid |
|---|---|---|
| **agents-work** | Native runtime design: fantasy loop with `StepCountIs(1)`, one durable queue job per step, messages persisted in Postgres, transactional enqueue (`InsertTx`) so a run row and its first job commit atomically. `jobqueue` interface confining River. Flows (trigger + agent + template) over pipelines/DAGs. Server-side fan-out/fan-in orchestration tools (`launch_agent`, `wait_for_agents`, cohorts) so reliability mechanics stay out of prompts. Per-run scratchpad memory with caps. Versioned loop registry so in-flight runs stay pinned across deploys. | Slack-first triggering as the core model; the legacy "brain in the pod" runtime; two write paths to run state. |
| **bezalel** | The dev-env tool contract: MCP over streamable HTTP, stateless-between-calls, bash with auto-background, edit/multiedit exact-match semantics, lazy LSP lifecycle, bearer auth, `GET /health`. Bezalel is the *in-env* half; the provider/lifecycle layer is explicitly ours to build. | Reimplementing in-env tools ourselves; stdio transport. |
| **switchboard** | Tools as an API design problem: meta-tools (search/execute) over raw tool floods, field compaction, session working-set context, pinned results, breadcrumb history for context-compression recovery, tool descriptions as UX. Hexagonal ports-and-adapters with a DI `Services` struct. | Baking integrations into the core — switchboard remains a composable sidecar. |
| **awesometree / ARP** | Agent lifecycle is a first-class protocol gap (MCP = agent→tool, A2A = agent→agent, neither does create/start/stop/destroy). Explicit agent state machine. Monotonically-decreasing privilege: spawned agents inherit tokens that can only narrow. Sessions as implicit spawn trees. | Desktop/WM coupling; workspace == git worktree as the *only* isolation unit. |
| **crush-modules** | We must own extension points in the loop itself: tools, hooks (background event processors), sub-agents-as-config, periodic prompts, OTLP instrumentation of sessions/steps/tool-calls. `fantasy.AgentTool` as the universal tool shape. The mockllm pattern: a scripted OpenAI-compatible *server* (network boundary, not in-process mock) for deterministic loop testing. | Compile-time plugin distribution as the primary extension mechanism (we're a service, not a binary). |
| **superlogical** | The framing itself: interactive, automatic, and production work share one durable substrate; multiplayer and reconnect-from-anywhere from day one; visible and controllable by people even when driven by software. | — |

---

## 2. Architecture

### 2.1 Core abstractions

```
Org ──────┬── Users           membership + roles; the billing and isolation boundary
          ├── Credentials     BYO inference (openai|anthropic|bedrock) + integration creds
          ├── ProviderInstances  where this org's dev envs run (hosted-eks | byo-k8s |
          │                      byo-nomad | local-via-cloudflared | local-docker)
          ├── Flows           org-scoped catalog
          └── Sessions[]

Session ──┬── EventLog        append-only, monotonic seq, the source of truth for observation
          ├── Participants    humans (via clients) and agents (via runs), presence, roles
          ├── AgentRun[]      durable fantasy loops; 0..n concurrent per session
          ├── DevEnv[]        provisioned environments; 0..n per session
          ├── Memory          structured key/value scratchpad, capped, namespaced
          └── FlowInvocations records of flows invoked into this session

Flow          named, versioned config: agents (prompt templates, models, tools) + env specs
              + wiring (which agent gets which env, spawn topology)

AgentRun      state machine: pending → running ⇄ awaiting → completed | failed | cancelled
              owns: message history (persisted), step audit rows, tool grants, token

DevEnv        state machine: requested → provisioning → ready ⇄ suspended → terminating → terminated | failed
              owns: provider ref, bezalel endpoint + token, spec (image, resources, workdir)
```

**Everything observable is an event.** Agent text deltas, tool calls/results, env
lifecycle transitions, participant joins, flow invocations, memory writes — all are typed
events appended to the session event log with a monotonic sequence number. Streaming,
multiplayer, history replay, and test assertions are all *the same mechanism*: subscribe
from seq N. This is the single most important design decision; it is what makes the
session durable and the system testable without mocks.

### 2.2 Components

```
                    ┌─────────────────────────────────────────────┐
  clients           │  ultrad (API server, stateless, N replicas) │
  react / gpui /    │  ConnectRPC: OrgSvc, BillingSvc, SessionSvc, │
  testclient  ──────│  AgentSvc, EnvSvc, FlowSvc, EventSvc (stream)│
                    └───────┬─────────────────────┬───────────────┘
                            │ Postgres            │ enqueue (tx)
                    ┌───────▼─────────┐   ┌───────▼───────────────┐
                    │ store           │   │ jobqueue (river impl) │
                    │ sessions, runs, │   └───────┬───────────────┘
                    │ envs, events,   │           │
                    │ flows, memory   │   ┌───────▼───────────────┐
                    └─────────────────┘   │ worker (N replicas)   │
                                          │ step jobs: fantasy    │
                                          │ loop, 1 step per job  │
                                          │ env jobs: provision/  │
                                          │ terminate/reconcile   │
                                          └───┬───────────┬───────┘
                                              │ LLM API   │ MCP-HTTP
                                       ┌──────▼─────┐ ┌───▼────────────────┐
                                       │ providers  │ │ DevEnv (per env)   │
                                       │ (fantasy)  │ │ bezalel sidecar    │
                                       └────────────┘ │ [+ switchboard]    │
                                                      └────────────────────┘
                                          envprovider instances: local(docker) | byo
                                          nomad | byo k8s | hosted EKS | cloudflared
                                          tunnel to user machines
```

- **`ultrad`** — stateless API server. All state in Postgres. Any replica can serve any
  session (event fan-out via Postgres LISTEN/NOTIFY keyed by session, falling back to
  poll-from-seq; NATS is a later optimization if needed, behind an interface).
- **`worker`** — runs queue jobs. An agent step job: load history → one fantasy step
  (`StepCountIs(1)`) → persist messages + step row + events → classify (continue /
  await / complete) → transactionally enqueue next step. Crash-safe by construction:
  execution-time state is disposable, `UNIQUE(agent_run_id, step_index)` makes
  redelivery idempotent.
- **`envprovider`** — our provider abstraction (the layer bezalel explicitly leaves to
  the consumer). Providers implement a small interface; a shared **conformance suite**
  makes new providers cheap and honest.
- **DevEnv runtime** — every env runs bezalel (in-env tools). Integration tools
  (github/linear/etc.) come from switchboard as an optional per-env or per-run sidecar,
  never baked into core.

### 2.3 We own the agent loop

Built directly on `charm.land/fantasy` (pinned; it is WIP upstream — track API churn):

- `fantasy.NewAgent(model, WithTools(...), WithSystemPrompt(...), WithStopConditions(fantasy.StepCountIs(1)))`
- `AgentStreamCall` callbacks (`OnTextDelta`, `OnToolCall`, `OnToolResult`,
  `OnStepFinish`) translate directly into session events — streaming is a side effect of
  event appending, not a separate channel.
- `PrepareStep` is our hook for context management: history compaction/summarization at
  size caps, model fallback chains, tool-set mutation per step.
- Message history: fantasy `[]Message` serialized with a versioned envelope
  `{"v":1,"messages":[...]}` per run.
- **Inference credentials are resolved per step** from the org's credential store
  (openai/anthropic/bedrock → the matching fantasy provider), decrypted in the worker
  at point of use. A run whose org lacks a working credential fails fast with a typed,
  user-actionable error — there is no platform fallback key.
- **Loop registry**: loops are versioned (`loop_kind`, `loop_version` stamped on the run
  at creation) so deploys never change in-flight run behavior.

**Tool tiers** (all `fantasy.AgentTool`):

1. **Native orchestration tools** (in-process, hit the store): `spawn_agent`,
   `wait_for_agents`, `run_agent_cohort` (fan-out/fan-in server-side),
   `provision_env`, `list_envs`, `terminate_env`, `session_memory_{get,set,list}`,
   `await_user` / `ask_user` (structured questions → awaiting state),
   `post_event` (agent-visible annotations).
2. **Env tools** (MCP-HTTP passthrough to bezalel): bash, view/write/edit/multiedit,
   glob/grep/ls, download/fetch, LSP diagnostics/references, jobs. A non-generic
   `mcpTool` adapter wraps discovered MCP tools as `fantasy.AgentTool`, namespaced per
   env (`env:main/bash`) when a run holds multiple envs.
3. **Integration tools** (MCP-HTTP passthrough to switchboard when attached):
   search/execute meta-tools pattern preserved.

**Advanced loop features** (post-MVP, from crush-modules learnings): hooks (background
event processors reacting to the session event log), periodic prompts, sub-agent
definitions as data (markdown/frontmatter-equivalent stored in flow configs), OTLP
tracing of runs/steps/tool-calls from day one.

### 2.4 Security & tenancy model

**Tenancy is row-level and total.** Every table carries `org_id` (directly or via its
session); every store method takes an org-scoped handle; every RPC resolves
`(identity → org membership → role)` before touching data. Cross-org access is
structurally impossible at the store layer (scoped queries), not just checked at the
handler layer — and a generated cross-tenant fuzz sweep (A7.3) proves it per-RPC.

From ARP: **monotonically decreasing privilege**. Every run holds a token scoped to
`(org, session, grants)`. `spawn_agent` mints a child token that can only narrow (subset
of tools, subset of envs, no credential inheritance for integrations — child envs get
their own switchboard credentials). Env bezalel tokens are per-env, SHA-256 hashed at
rest with encrypted cleartext, rotated on env restart.

**Credential store** (`credentials`): org-scoped, kind-tagged (`inference:openai`,
`inference:anthropic`, `inference:bedrock`, `provider:kubeconfig`, `provider:nomad`,
`integration:*`), AES-GCM encrypted at rest with a KMS-backed master key, decrypted only
in workers at point of use, never serialized into events, run history, logs, or traces
(enforced by a redaction layer + tests). Inference is **always** on org credentials —
the platform never fronts LLM spend.

### 2.5 Data model (Postgres, goose migrations, pgx)

```sql
orgs            (id, name, plan, stripe_customer_id, created_at)
users           (id, email, display, created_at)
org_members     (org_id, user_id, role owner|admin|member, PRIMARY KEY (org_id, user_id))
credentials     (id, org_id, kind, name, enc_payload bytea, created_by, rotated_at,
                 UNIQUE(org_id, kind, name))
provider_instances (id, org_id, kind hosted_eks|byo_k8s|byo_nomad|tunnel_local|local_docker,
                 name, config jsonb, credential_id, state, last_healthy_at,
                 UNIQUE(org_id, name))
env_usage       (id, org_id, env_id, provider_instance_id, started_at, ended_at,
                 seconds, rate_class hosted|byo, billed_period)  -- metering ledger
sessions        (id, org_id, title, created_at, archived_at, metadata jsonb)
session_events  (session_id, seq bigint, ts, actor_type, actor_id, kind, payload jsonb,
                 PRIMARY KEY (session_id, seq))          -- seq via per-session counter
participants    (session_id, participant_id, kind human|agent, display, joined_at, last_seen)
agent_runs      (id, session_id, parent_run_id, flow_invocation_id, state, loop_kind,
                 loop_version, model_config jsonb, history jsonb, token_hash, ...)
agent_run_steps (agent_run_id, step_index, tokens_in, tokens_out, finish_reason, ts,
                 UNIQUE(agent_run_id, step_index))
dev_envs        (id, session_id, state, provider_instance_id, spec jsonb, endpoint,
                 token_hash, token_enc, created_by_run_id, ...)
flows           (id, org_id, name, version, definition jsonb, UNIQUE(org_id, name, version))
flow_invocations(id, session_id, flow_id, params jsonb, invoked_by, ts)
session_memory  (session_id, key, value jsonb, updated_by, updated_at,
                 PRIMARY KEY (session_id, key))          -- 200-key cap, 64KiB/value, advisory locks
```

### 2.6 Client API — ConnectRPC (protobuf)

Chosen because it is the only option that satisfies the UI-agnostic constraint without
inventing a codegen pipeline: strictly typed schemas (`buf` lint + **breaking-change CI
gate**), first-class **Go** (connect-go), **TypeScript** (connect-es, works in browsers
over HTTP/1.1+2 — no grpc-web proxy), and **Rust** (tonic/prost speak the same protos;
Connect protocol is gRPC-compatible). Server streaming RPCs give us event streams
natively.

```protobuf
service OrgService      { CreateOrg, GetOrg, InviteMember, ListMembers, SetRole,
                          PutCredential, ListCredentials /* names+kinds only */,
                          DeleteCredential,
                          RegisterProvider, ListProviders, DeleteProvider }
service BillingService  { GetUsage /* seat + env-seconds by period */, GetSubscription,
                          CreateCheckoutSession /* Stripe */ }
service SessionService  { CreateSession, GetSession, ListSessions, ArchiveSession,
                          Join, Leave, Heartbeat /* presence */ }
service EventService    { Subscribe(SubscribeRequest{session_id, from_seq})
                            returns (stream SessionEvent);
                          Append(...) /* human messages, annotations */ }
service AgentService    { StartRun, PromptRun /* wake awaiting */, CancelRun,
                          GetRun, ListRuns }
service EnvService      { ProvisionEnv, GetEnv, ListEnvs, TerminateEnv,
                          ExecPreview /* human runs a command in an env, as an event */ }
service FlowService     { PutFlow, GetFlow, ListFlows, InvokeFlow }
```

Rules: every mutation returns the event seq it produced (so clients can await
consistency); `SessionEvent` is a oneof of typed payloads (no `google.protobuf.Struct`
grab-bags in the public API); multiplayer = N concurrent `Subscribe` streams + presence
events; reconnect = `from_seq`.

### 2.7 Queue — river behind a type-safe seam

```go
package jobqueue // no river imports here

type Job interface{ Kind() string }
type Enqueuer interface {
    EnqueueTx(ctx context.Context, tx pgx.Tx, job Job, opts ...Opt) error
}
type Worker[J Job] interface {
    Work(ctx context.Context, job J) error
}
type Registry interface{ Register(kind string, worker any) }
```

Implementations: `jobqueue/river` (prod) and `jobqueue/inproc` (in-memory,
same-transaction-aware, for fast tests). Both run the **same conformance suite**
(transactional enqueue visibility, at-least-once redelivery, retry/backoff contract).
Swapping backends later = new package + passing conformance suite. Type safety via
generics at registration; job payloads are plain structs with JSON tags.

### 2.8 Env providers

```go
package envprovider

type Provider interface {
    Name() string
    Provision(ctx context.Context, spec EnvSpec) (Handle, error) // async: returns handle, poll status
    Status(ctx context.Context, h Handle) (EnvStatus, error)
    Terminate(ctx context.Context, h Handle) error
    Endpoint(ctx context.Context, h Handle) (URL, error)          // bezalel MCP-HTTP endpoint
}
```

Providers are instantiated per **provider instance** — an org-scoped registration of
*where envs run*, carrying kind, config, and (for BYO kinds) a credential reference:

- **local_docker** — docker containers (bezalel image + optional switchboard), for dev
  and CI (platform-internal; not offered to hosted tenants).
- **byo_nomad** — job per env on the org's nomad cluster (org-supplied nomad creds).
- **byo_k8s** — pod per env on the org's cluster (org-supplied kubeconfig).
- **hosted_eks** — pod per env on *our* EKS, platform-managed, metered at the hosted
  rate class (the upsell: zero setup, we handle capacity).
- **tunnel_local** — the org runs `ultra env-agent` on their own machine: a local
  docker provider that dials out via cloudflared, so the platform reaches env endpoints
  through the tunnel with no inbound firewall holes.

A **reconciler job** (queue-scheduled) drives desired→actual state and detects dead envs
(bezalel `/health`). A **provider conformance suite** (provision → health → exec bash →
edit file → terminate → verify gone) runs against every provider; local in CI always,
nomad/k8s in CI via kind + nomad-dev-agent, plus nightly against real clusters.

**Metering:** every env's `ready → terminated` wall time is recorded in the `env_usage`
ledger (opened at `EnvReady`, closed at terminal state, heartbeat-ticked so a crash
never produces an unbounded open interval), tagged with the instance's rate class
(`hosted` vs `byo` — BYO compute is the org's own; we bill a small orchestration rate or
zero per plan). This ledger is the billing source of truth.

### 2.8a Monetization model

- **Seats:** per active user per org per month (Stripe subscription; `org_members` is
  the seat count, owners choose per-seat tier).
- **Env usage:** metered `env_usage.seconds` by rate class, invoiced monthly via Stripe
  metered billing. Hosted EKS is the premium rate class; BYO (k8s/nomad/tunnel-local)
  is free or near-free, making hosted the convenience upsell rather than a lock-in.
- **Inference:** never resold — orgs bring their own OpenAI/Anthropic/Bedrock
  credentials. We surface per-run token usage (already in step rows) for their own
  cost attribution, but the LLM bill is theirs.

### 2.9 Repo layout

```
/proto/ultra/v1/            *.proto, buf.yaml, buf.gen.yaml
/gen/                       generated go / ts / rust client+server stubs (committed)
/                           root Go module github.com/aleksclark/ultralogical
  store.go, session.go, org.go, ... domain types + Store interface (Ben Johnson layout)
  /postgres/                Store impl, migrations/
  /secrets/                 credential encryption (AES-GCM + KMS keyring), redaction
  /billing/                 metering ledger, Stripe adapter behind an interface
  /jobqueue/  /jobqueue/river/  /jobqueue/inproc/
  /loop/                    fantasy loop, loop registry, native tools, mcpTool adapter
  /envprovider/  /envprovider/local/  /envprovider/nomad/  /envprovider/k8s/
                 /envprovider/tunnel/ (cloudflared local connector, server side)
  /cmd/ultra-env-agent/     user-side local provider + cloudflared dialer
  /server/                  connect handlers, event fan-out
  /cmd/ultrad/  /cmd/worker/  /cmd/ultra/ (CLI)
/testkit/                   testclient (Go, consumes gen/go client), modelscript server,
                            harness (compose up, seed, assert-on-events)
/clients/ts/                published TS package wrapping gen/ts
/clients/rust/              rust crate wrapping tonic-generated stubs
/ui/web/                    React SPA: vite + shadcn/ui + tailwind; /ui/web/e2e/ playwright
/ui/gpui/                   rust gpui app (Phase 8)
/e2e/                       cross-stack golden suites
```

---

## 3. Testing strategy (the drift defense)

**Principle: tests exercise the real system through its real boundaries.** No in-process
mocks of our own components, ever. The layers:

1. **Unit tests** — pure logic only (event encoding, token scoping, compaction). No
   store mocks: store-dependent logic tests run against real Postgres
   (dockertest/testcontainers, per-test schema).
2. **Conformance suites** — shared black-box suites run against every implementation of
   a seam: `jobqueue` (river + inproc), `envprovider` (local + nomad + k8s), `Store`.
   This is what makes "easily swapped while remaining type-safe" true rather than
   aspirational.
3. **Functional API suite (`testkit`)** — the first-line suite. A **test client** built
   on the *generated Go client* (same artifact users get) drives a fully real stack:
   real `ultrad`, real `worker`, real Postgres, real river, real bezalel containers via
   the local provider. The **only** substituted component is the LLM vendor: the
   **modelscript server** — a standalone OpenAI-compatible HTTP server (crush-modules
   mockllm pattern) that serves scripted multi-turn conversations at the network
   boundary. The loop, streaming, tool execution, and persistence paths are 100% real.
   Assertions are made against the event log (`Subscribe from_seq 0` and assert the
   typed event sequence) — the same API users consume.
4. **Golden live suite** — a small tagged suite (`//go:build live`) running nightly
   against a real LLM provider with a cheap model, verifying the modelscript contract
   hasn't drifted from reality.
5. **Per-UI golden functional suites** — Playwright for the React SPA, gpui test
   harness for the rust app, each driving the same real backend stack via the harness.
   These verify the UI renders/controls real sessions; they do not re-test backend
   logic.
6. **Drift gates in CI** — `buf breaking` on protos; generated clients rebuilt and
   diffed (fail on uncommitted drift); conformance suites mandatory for any new
   provider/queue impl; the functional suite is required for merge.

`testkit/harness` gives every suite one entrypoint: `harness.Up(t)` → compose stack
(ultrad, worker, postgres, modelscript) with random ports, returns a connected
testclient. Target: full functional suite < 5 min.

---

## 4. Key risks & mitigations

| Risk | Mitigation |
|---|---|
| fantasy API churn (WIP upstream) | Pin exact version; isolate behind `/loop`; loop registry versioning means upgrades only affect new runs. |
| One-job-per-step latency | Acceptable for v1; batch N steps per job behind a wall-clock budget later — the seam already allows it. |
| Event log growth | seq-indexed, append-only; retention/archival in Phase 7; deltas can be coalesced at read time for history rendering. |
| Rust Connect support maturity | tonic speaks gRPC to ultrad (connect-go serves both protocols); A8.1 verifies parity early in Phase 8, fallback is pure gRPC. |
| LISTEN/NOTIFY fan-out limits | Interface-isolated; poll-from-seq fallback is always correct; NATS/Redis swap-in is additive. |
| BYO creds are a breach magnet | KMS-envelope encryption, decrypt only in workers at point of use, redaction layer over events/logs/traces with tests that grep for planted canary secrets (A1.7, A2.4, A6.1). |
| Tunnel-local envs are flaky/slow | Tunnel provider is capability-flagged; reconciler treats tunnel loss as `suspended` not `failed` (resumable); tool-call deadlines already structural (Phase 2). |
| Metering disputes | `env_usage` intervals are derived from the same event log users can see — the bill is replayable from their own session history. |
| Stripe coupling | `/billing` isolates Stripe behind an interface; the ledger is ours, Stripe only invoices it. |
| Drift during agentic development | The whole Section 3 apparatus: conformance suites, generated-client diff gates, event-log-asserting functional tests required for merge. |

## 5. Explicit non-goals (v1)

- Terminal multiplexing / PTY sharing (superlogical's product — we build the session
  substrate, not the terminal).
- Building our own in-env toolset (bezalel's job) or integration catalog (switchboard's job).
- Slack/GitHub/webhook triggers for flows (post-v1; flows are session-invoked first).
- A2A/ACP interop (post-v1; the ARP lessons are encoded in our lifecycle API instead).
- Platform-fronted inference (reselling LLM tokens) — orgs always bring their own keys.
- Cross-org sharing/marketplace of flows (post-v1; org-scoped only).
- Usage-based seat proration, enterprise SSO/SCIM (post-v1; OIDC covers auth).
