# Phase E6 — Ceiling proof: curri-agents nativeagent migration

**Objective:** Put curri-agents' **nativeagent** path on the core: core
sessions/runs/events replace FlowRun/AgentRun persistence + NATS-based
observation for native runs, and `runtime.Runtime` (k8s/local provisioning)
is replaced by core resource providers. This proves the core's **ceiling**:
real k8s provider at production scale, high event throughput, multi-turn
awaiting conversations (Slack), and orchestration under load.

**Depends on:** E4; overlaps E5 after E5's first week (gap discoveries
serialize through core patch releases).
**Duration guess:** 3–4 weeks, in the curri-agents repo, PR+CI per repo
rules (RWX, never push master).

**Boundary (D2):** the Crush-in-pod path is **out of scope** — it stays on
curri's existing supervisor machinery until nativeagent covers its use
cases, and is never adapted onto the core. This phase migrates the native
(Fantasy) runtime only.

---

## Scope

- curri keeps: Flow model (trigger + template + wiring), Slack/GitHub/
  Sentry/webhook/cron triggers, FlowSlack bridge, admin SPA, AI-gateway
  credential federation, PR-description sync — all consumer vocabulary.
- curri replaces (native path only): AgentRun persistence + step jobs,
  message history storage, run event fan-out (NATS for run observation),
  `nativeagent/runtime` provisioning, orchestrator store plumbing.
- Deployment: core stack in the agents EKS cluster; curri's Overmind talks
  to it via the Go SDK. Curri is tenant `curri` (staging + prod tenants).

## Mapping

| curri today (native path) | on the core |
|---|---|
| FlowRun → AgentRun rows + orchestrator store | FlowRun stays curri-side (trigger provenance); each native AgentRun becomes a core run in a core session, labels: `app=curri, flow=<name>, flow_run=<id>, trigger=slack|github|…` |
| `runtime.Runtime.Provision/Teardown` (k8s/local) → pod with bezalel+switchboard | `provision_resource(kind=dev_env)` on tenant `curri`'s `byo_k8s` provider instance; **likely gap G3:** curri's ProvisionSpec (RepoURL/Ref/HeadSHA checkout, RoleEnv, runtime token, co-located switchboard) must be expressible in the `dev_env` spec schema — expect spec-schema extensions: init/checkout hook, sidecar/aux endpoints, injected env |
| NATS JetStream for run-event observation | core event log + Subscribe; **NATS stays** for Slack↔bridge messaging where it's genuinely a message bus. **Likely gap G4:** Subscribe fan-out throughput — validate the LISTEN/NOTIFY+poll design at curri's event volume; the eventbus interface already anticipates a NATS-backed impl if needed (plan/index: "NATS is a later optimization… behind an interface") |
| Multi-turn Slack conversations (ACP `awaiting`) | core run `awaiting` state + `RunService.Answer`; FlowSlack bridge translates Slack replies → Answer |
| Model fallback chains (cfaigrewriter/CF AI Gateway) | `ModelConfig.Fallbacks` + tenant credentials; **likely gap G5:** per-developer federated gateway tokens = per-run credential selection — may need run-level credential override, core-side, tested |
| Step audit rows / admin visibility | core events + run/step queries via SDK; admin SPA reads through Overmind facade |

## Work items

- **T6.1** Deploy core to EKS (staging first): cored + coreworker,
  RDS/Postgres per env; register `byo_k8s` provider instance for the
  agents cluster; tenant/keys/credentials bootstrap in IaC (`infra/`).
- **T6.2** Gap resolution in core (expected G3 spec schema, G4 throughput,
  G5 credentials; ledger `phase_e6_gaps.md`). G4 gets a load test in the
  core repo: sustained event append + N subscribers at curri's measured
  peak (pull the number from SigNoz; record it), regression-compared to
  `agent_docs/throughput_baseline.md` methodology.
- **T6.3** curri: nativeagent runner rewires to SDK (StartRun, Subscribe,
  Answer); `nativeagent/runtime` package deleted in favor of resource
  provisioning; orchestrator store trimmed to FlowRun bookkeeping +
  core-run references. Feature flag per-flow: `runtime=core|legacy-native`.
- **T6.4** FlowSlack bridge: awaiting-state translation onto core runs;
  multi-turn e2e in curri's cluster suite (slackemu) against a real core
  stack.
- **T6.5** Cutover: staging soak on real flows → prod flows migrated
  incrementally (cron/webhook flows first, Slack-conversational last) →
  legacy-native path deleted after soak.
- **T6.6** Load/chaos evidence: kill coreworker mid-step (redelivery
  idempotency at curri scale), kill cored mid-subscribe (SDK resume), pod
  eviction of a dev_env mid-run (reconcile → typed failure event → curri
  surfaces it in Slack).

---

## Acceptance criteria

- **A6.1** curri suites green with core backend: unit, integration, cluster
  (`CLUSTER_TEST=1`), e2e — including the Slack harness multi-turn
  conversation against real core.
- **A6.2** A production-shaped flow (GitHub PR trigger → native agent →
  repo checked out in dev_env → bezalel edits → PR updated) runs end-to-end
  on staging with all observation through core Subscribe; the admin SPA
  shows live progress through the Overmind facade.
- **A6.3** Multi-turn Slack: a conversational flow round-trips ≥3 turns via
  awaiting/Answer with no message loss across an Overmind restart
  mid-conversation.
- **A6.4** Throughput: core sustains curri's measured peak event rate with
  N concurrent subscribers at < the latency budget recorded in the gap
  ledger; numbers recorded against the throughput baseline methodology. If
  LISTEN/NOTIFY+poll fails the budget, the NATS eventbus impl lands behind
  the existing interface with the same functional suite passing.
- **A6.5** Chaos: T6.6's three kill scenarios produce converged state and
  correct events (no orphaned pods — provider lister leak check; no
  duplicate steps — `UNIQUE(run, step_index)` evidence; no stream gaps).
- **A6.6** Credential federation: two developers trigger flows and LLM
  traffic is attributed to their respective gateway tokens (G5 evidence).
- **A6.7** `nativeagent/runtime` and native-run NATS observation code
  deleted from curri; grep-clean; RWX CI green on master after final merge.
- **A6.8** Prod soak: two weeks, defect log in audit, no P1 attributable to
  the migration.

## Test coverage

| Behavior | Test | Where |
|---|---|---|
| Native flows on core | curri cluster + e2e suites | curri repo |
| Multi-turn awaiting via Slack | curri slack-harness cluster test | curri repo |
| dev_env spec extensions (G3) | core provider conformance + functional | core repo |
| Event throughput (G4) | core load test vs baseline | core repo |
| Run-level credentials (G5) | core functional | core repo |
| Chaos triplet | curri cluster tests + core convergence suite | both |
| K8s provider at prod scale | staging soak + lister leak checks | ops |

## Exit audit

`phase_e6_audit.md`: A6.1–A6.8 with evidence; gap ledger; final
consolidation accounting — LOC deleted from curri, LOC deleted from primer,
core LOC, suites' runtimes — versus the E0 baseline. This closes the
extraction; further work is normal roadmap.
