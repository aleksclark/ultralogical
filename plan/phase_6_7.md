# Phase 6.7 — Complete audited Phase 0–6 gaps

This phase owns every incomplete item discovered by the independent
Phase 0–6 audit. Nothing below is considered delivered without real Go,
dark-mode shadcn, and dark-mode GPUI evidence.

## Priority 0: correctness/security

- Agent spawn/cohort/wait tools with narrowing grants, durable idempotency,
  parent resumption, and timeout.
- Dispatch-time tool/environment authorization and PermissionDenied events.
- Multi-replica harness proof.
- Real Phase 2 durability/auth/reconciliation/metering assertions.
- Flow environment/topology/provenance and structured validation.
- Replace provider-kind loopback aliases with real Kubernetes/Nomad/tunnel
  implementations and hosted isolation.
- Switchboard integration, real summarization compaction, sticky fallback,
  durable hooks, periodic firing, and OTLP.

## Priority 1: client completeness

- Complete shadcn web controls and dark GPUI views/actions for every public
  API, including flows, providers, run trees, memory, usage, automation, and
  advanced-loop state.
- Capability-specific Playwright and GPUI tests; no direct-RPC desktop tests
  may be used as GPUI evidence.

## Priority 2: evidence/infrastructure

- Strengthen matrix validation to require Go + web + GPUI evidence and bounded
  assertions.
- Add panic redelivery, codegen gate mutation tests, provider conformance
  parity, live-LLM nightly, provider CI legs, and documentation examples.

## Exit criteria

The audit list in `agent_docs/phases_0_6_audit.md` has no unowned item. Each
completed item has truthful matrix evidence; deferred items remain explicitly
open and unclaimed. Full Go, Playwright, GPUI/Rust, conformance, codegen,
lint, and coverage gates pass.
