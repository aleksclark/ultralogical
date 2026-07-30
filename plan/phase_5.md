# Phase 5 — BYO & hosted providers: nomad, k8s, cloudflared local, hosted EKS

**Duration:** 2–3 weeks · **Depends on:** Phase 2 · **Parallelizable with:** Phase 4

## Goal

Prove the provider seam and ship the tenancy-critical env story: orgs run dev envs
**where they choose** — their own k8s cluster (BYO kubeconfig), their own nomad cluster
(BYO nomad creds), their own machine (`ultra env-agent` connected via cloudflared), or
our **hosted EKS** (the zero-setup upsell, metered at the hosted rate class). The Phase 2
conformance suite passes unmodified on every kind, and the "easy integration points for
new providers" promise is demonstrated with a documented sub-200-LOC walkthrough.

## Scope

**In:**
- `envprovider/nomad`: one nomad job per env (bezalel task + optional sidecars); used
  by both `byo_nomad` instances (org creds) and internal deployments.
- `envprovider/k8s`: one pod per env (the agents-work pattern); used by both
  `byo_k8s` (org kubeconfig) and `hosted_eks` (platform kubeconfig, platform-managed
  cluster, hosted rate class, per-org namespaces + quotas).
- `envprovider/tunnel` + `cmd/ultra-env-agent`: user-side local docker provider that
  dials out via cloudflared; server-side provider that routes provision/status/MCP
  traffic through the tunnel.
- BYO credential onboarding: `RegisterProvider` accepts kubeconfig / nomad token via
  the credential store (encrypted, validated with a dry-run capability check at
  registration).
- Endpoint resolution per platform; instance selection via `EnvSpec.provider_instance`.
- Provider capability flags (e.g. `SupportsRestartPreservingWorkspace`,
  `ToleratesDisconnect`) so conformance adapts without weakening.
- CI: kind cluster + nomad dev agent legs + a loopback-tunnel leg; nightly against real
  EKS.
- UI: provider onboarding flows (paste kubeconfig / nomad address+token / guided
  env-agent setup with copy-paste command), instance health display, hosted-EKS
  one-click enable.
- Docs: `docs/providers.md` + the stub-provider walkthrough + BYO onboarding guides.

**Out:** autoscaling/bin-packing policy, spot handling, multi-cluster per instance
(post-v1). Suspend/resume still reserved (tunnel disconnect maps to `suspended`).

## Design details

### Nomad provider (`byo_nomad`)

- `Provision`: submit a parameterized job (`ultralogical-env-<id>`) against the
  instance's nomad address using the org's ACL token (decrypted at point of use),
  bezalel task with the env token as a template-injected secret, resources from
  `EnvSpec`. Restart-preserving workspace is a capability flag, not assumed.
- `Endpoint`: allocation discovery → advertised address of the bezalel port. Works
  against a bare nomad API; Consul optional. Requires the org's cluster to allow
  inbound from our workers **or** the org fronts it with their own cloudflared — the
  onboarding doc covers both; registration dry-run verifies reachability end-to-end.
- `Status`: map job/alloc states. `Terminate`: deregister, purge.

### K8s provider (`byo_k8s` and `hosted_eks`)

- One provider implementation, two instance kinds differing only in config source and
  rate class:
  - `byo_k8s`: org-supplied kubeconfig (credential store); envs in a
    `ultralogical-envs` namespace in *their* cluster; registration dry-run: SSAR
    permission check + create/delete a canary pod.
  - `hosted_eks`: platform kubeconfig; **namespace per org** (`org-<id>`) with
    ResourceQuota + LimitRange + NetworkPolicy (no cross-namespace traffic, egress
    allowlist); metered `hosted`; per-plan concurrent-env and resource ceilings
    enforced at provision time with typed quota errors.
- `Provision`: pod (`ultralogical-env-<id>`, labeled) + token Secret + per-env Service;
  workspace `emptyDir` (or PVC when capability-flagged). bezalel `/health` as
  readinessProbe, so `ready` == probe-passing.
- `Endpoint`: in-cluster Service via cluster DNS for co-located workers (hosted_eks);
  for byo_k8s, the API-server proxy path or org-side cloudflared per onboarding —
  resolver choice is instance config, not caller concern.

### Tunnel provider (`tunnel_local` + `ultra env-agent`)

The user runs one binary on their machine:

```
$ ultra env-agent --org myorg --token <registration-token>
  ✓ docker reachable
  ✓ tunnel established (cloudflared, outbound-only)
  ✓ registered as provider instance "aleks-laptop"
```

- **Agent side** (`cmd/ultra-env-agent`): embeds the Phase 2 local-docker provider and
  exposes it over a small authenticated HTTP control API (provision/status/terminate/
  endpoint-proxy); starts a cloudflared tunnel (embedded `cloudflared` invocation or
  library) so the control API and every env's bezalel endpoint are reachable at
  `https://<instance>.tunnel.<ourdomain>` — outbound-only from the user's machine, no
  inbound firewall holes, TLS terminated by Cloudflare.
- **Server side** (`envprovider/tunnel`): implements `Provider` by calling the agent's
  control API through the tunnel; MCP tool calls to envs route the same way. Auth is
  mutual: the agent authenticates registration with an org-scoped token; the platform
  signs control-API requests so a leaked tunnel URL is useless.
- **Disconnect semantics**: tunnel loss ⇒ instance unhealthy ⇒ its envs move to
  `suspended` (not `failed`); reconnection resumes them (capability flag
  `ToleratesDisconnect`). Metering intervals pause on suspend — users aren't billed
  (even at byo rates) for disconnected laptops. Tool calls during suspension return a
  structured retriable error.
- Registration UX: the UI issues a one-time registration token and shows the exact
  command; the instance appears with live health as soon as the agent connects.

### Provider registry & instance plumbing

```go
// envprovider/registry.go
func Register(kind string, factory Factory) // called from provider packages
// Factory: func(instanceConfig json.RawMessage, creds secrets.Decryptor) (Provider, error)
```

Deployment config whitelists enabled kinds. `RegisterProvider` validates: kind enabled,
config schema, credential dry-run (capability probe). Instance health maintained by the
reconciler; unhealthy instances refuse new provisions with typed errors but never lose
existing env records.

### CI topology

- **Local leg** (every PR): docker conformance (green since Phase 2).
- **kind leg** (every PR): conformance as `byo_k8s` + hosted-mode namespace/quota
  tests. Budget < 8 min.
- **nomad leg** (every PR): `nomad agent -dev`, conformance as `byo_nomad`.
- **tunnel leg** (every PR): `ultra env-agent` in loopback-tunnel mode (a fake tunnel
  transport implementing the same dialer interface — cloudflared itself is exercised
  nightly, its *routing semantics* every PR).
- **Nightly** (env-gated secrets): real EKS (hosted mode, quotas), real cloudflared
  tunnel, provision/terminate soak (20 envs), alerting on failure.

## Work breakdown

1. Capability flags + conformance parameterization (flags swap *how*, never *whether*,
   a claimed behavior is verified).
2. Provider registry + instance factory plumbing + registration dry-run framework.
3. Nomad provider + conformance green on dev agent.
4. K8s provider + conformance green on kind; hosted mode (namespaces, quotas,
   NetworkPolicy, rate class).
5. `ultra env-agent` + tunnel provider + loopback transport + suspend/resume
   semantics + metering pause.
6. BYO onboarding: credential intake, dry-run validation, UI flows, env-agent
   registration UX.
7. CI legs (kind, nomad, tunnel) + nightly (EKS + cloudflared).
8. Stub-provider walkthrough (`docs/providers.md`) — written by building
   `envprovider/static` and counting lines.
9. Tests A5.2–A5.7 parameterized runs.

## Acceptance tests

- **A5.1 — Conformance everywhere.** The Phase 2 conformance suite passes **unmodified**
  (same test code, different factory) on local_docker, byo_nomad (dev agent), byo_k8s
  (kind), hosted_eks (kind in hosted mode), and tunnel_local (loopback transport).
  Capability-flagged steps run where claimed, skip-with-reason otherwise.
- **A5.2 — Real-work parity.** A2.2 (agent provisions env + does git work) and A2.3
  (env survives worker SIGKILL) re-run parameterized over all five kinds. Shared test
  body; only harness instance config differs.
- **A5.3 — Mixed-provider session.** One session, two envs on different instances
  (local_docker + kind). A single run uses namespaced tools from both in consecutive
  steps; results are real and correctly attributed.
- **A5.4 — Failure surfaces cleanly.** Unreachable nomad/k8s API at provision →
  `failed` + structured `EnvFailed` within deadline; no stuck `provisioning` rows after
  one reconcile; the invoking run receives a structured tool error, not a hang.
  Registration with bad creds (invalid kubeconfig, expired nomad token, wrong
  registration token) fails at dry-run with actionable messages — nothing persisted.
- **A5.5 — New-provider DX.** Following `docs/providers.md` verbatim, the `static`
  stub provider is < 200 LOC (CI line-count check) and passes conformance.
- **A5.6 — Tunnel lifecycle.** With env-agent connected (loopback leg): provision →
  agent runs the container locally → agent-side file writes visible via MCP through
  the tunnel. Kill the tunnel: instance unhealthy, envs `suspended`, metering interval
  paused (assert ledger), tool call returns retriable error. Reconnect: envs resume,
  same workspace contents, metering resumes, a queued step completes. Platform-side
  requests without the request signature are rejected at the agent.
- **A5.7 — Hosted isolation & quotas.** Two orgs on hosted_eks (kind hosted mode):
  pods land in per-org namespaces; a NetworkPolicy check proves org A's env cannot
  reach org B's env service; org at its concurrent-env ceiling gets a typed quota
  error (no pod created); usage ledger rows carry `hosted` rate class.

## Exit criteria

- A5.1–A5.7 green; kind + nomad + tunnel legs required for merge; nightly EKS +
  cloudflared job scheduled.
- `docs/providers.md` (walkthrough, capability flags) + BYO onboarding guides
  (kubeconfig, nomad, env-agent) published; onboarding guides verified by the harness
  actually following them (scripted).
- Flow examples updated: one flow pinned to a k8s instance, exercised in the kind leg.
