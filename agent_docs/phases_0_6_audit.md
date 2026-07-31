# Phases 0–6 implementation audit

The Phase 6.7 audit found material planned work still incomplete. Priority
items are tracked in `plan/phase_6_7.md`.

## Phase 6.7 implementation status

Completed in this iteration: panic redelivery conformance for every queue backend, dispatch-time environment/tool filtering,
per-native-tool grants, environment authorization on termination,
PermissionDenied event emission, non-busy provisioning polling, child-agent
spawn with transactional enqueue and narrowed grants, durable wait records,
terminal child fan-in with exactly-once parent resumption, and grant regression
coverage.

All other items below remain open and must not be described as complete.

## Incomplete by phase

- **0:** panic-redelivery conformance and deliberate codegen-gate mutation tests.
- **1:** browser incremental rendering proof, logs/RPC redaction sweep, complete shadcn conversion, one-command full dev stack.
- **2:** independent Docker durability verification, restart token rotation/cache invalidation, continued-run failure behavior, complete metering/tenancy tests, usage UI, full provider conformance cases.
- **3:** spawn/wait/cohort tools, grant enforcement, multi-replica harness, full memory/concurrency evidence, run-tree/lanes UI, security docs and throughput baseline.
- **4:** flow environments/topology/readiness gating, provenance on runs/envs, structured validation, CLI, shadcn/GPUI flow clients, examples/docs.
- **5:** real Kubernetes/Nomad/tunnel providers, hosted namespace isolation/quotas, credential dry-runs, required provider CI topology, onboarding guides/static-provider proof.
- **6:** Switchboard, summarization compaction, sticky fallback/per-step model audit, durable cursor hooks/auto-title, prompt firing scheduler/native tool, OTLP, advanced shadcn/GPUI UI, live nightly.

## Evidence integrity issues

Current Rust evidence mostly drives tonic directly rather than rendered GPUI.
Several web tests assert controls or final states without the claimed
lifecycle/failure/replay behavior. Coverage validation checks declarations,
not assertion semantics. Phase 6.7 must not broaden matrix claims until the
actual behavior and application-path tests exist.
