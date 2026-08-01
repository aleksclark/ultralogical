# Phase 10 — Real remote environment providers

**Duration:** 3–4 weeks · **Depends on:** Phase 9

## Goal

Close every audited Phase 5 gap by replacing provider-kind aliases with real Kubernetes, Nomad, hosted-EKS, and tunnel-local adapters. A provider exists only when its own control plane creates, authenticates, health-checks, reconnects, and destroys an isolated Bezalel environment under the shared conformance suite.

## Scope

**In:**

- Real `byo_k8s`, `hosted_eks`, `byo_nomad`, and `tunnel_local` adapters behind `envprovider.Provider`.
- Provider-specific credentials/config validation and dry-run health checks.
- Namespace/job isolation, quotas, network/auth boundaries, endpoint discovery, restart, reconcile, and cleanup.
- kind and Nomad development-agent CI legs, a real tunnel process in CI, and scheduled real-cluster checks.
- Static-provider wiring proof, onboarding, diagnostics, and dark shadcn/GPUI provider management.
- **Inherited from Phase 9** (see agent_docs/phase_9_audit.md, "Open items"):
  behavioral proof that a provider incapable of serving environment health
  rejects a flow that declares it, and a deep-link client path that opens one
  invocation directly rather than only through a session's list.

**Out:** multi-region scheduling, cluster autoscaling, enterprise networking, and production billing enforcement (Phase 12).

## Non-negotiable anti-alias rules

- A Kubernetes provider must create and inspect Kubernetes resources through a Kubernetes client. It cannot delegate lifecycle to local Docker.
- A Nomad provider must register and inspect Nomad jobs/allocations through Nomad APIs. It cannot delegate lifecycle to local Docker.
- A tunnel provider must establish and supervise an outbound tunnel and reach a remote env agent through it. Rewriting an endpoint to loopback is not an implementation.
- `hosted_eks` may share Kubernetes adapter code with `byo_k8s`, but it must use platform credentials, enforced org namespace isolation, quotas, and hosted rate classification.
- Provider conformance assertions cannot branch away core behaviors. Capability-specific skips must be documented, narrow, and may not skip provision, auth, health, tool execution, restart/reconcile, or termination.

## Required implementation sequence

1. Define the common conformance contract and provider-specific capability manifest before implementing adapters. Include leak detection and failure injection.
2. Add credential/config schemas with redacted diagnostics. Registration performs a read-only dry run against the target control plane and returns typed field errors without persisting invalid secrets.
3. Implement Kubernetes resource creation with deterministic labels, owner identity, resource requests/limits, service/endpoint discovery, readiness probes, scoped token injection, and idempotent deletion.
4. Add hosted-EKS policy: per-org namespace or equivalent hard boundary, service account/RBAC, network policy, resource quota, concurrent-env quota, and hosted metering class.
5. Implement Nomad jobs with deterministic identity, task resources, Bezalel health, endpoint discovery, token injection, allocation replacement handling, and idempotent purge.
6. Implement tunnel-local with a real env-agent registration and outbound cloudflared-compatible process, authenticated lease/heartbeat, suspended state on tunnel loss, endpoint renewal, and explicit teardown.
7. Reconcile externally deleted, replaced, unreachable, and partially created resources without duplicates or cross-org impact.
8. Add required CI: kind and Nomad per PR, real tunnel per PR, scheduled pinned-version and real-cluster matrices. CI must prove the intended adapter was used by inspecting provider-native resources.
9. Build provider registration, credential dry-run, health, quota, environment location, diagnostics, and remediation in dark shadcn and GPUI applications.
10. Execute onboarding guides from a clean environment and verify static-provider configuration works without database registration where documented.
11. Make provider capability a real, queryable property of a registration rather than a compile-time table. A flow declaring an environment policy a provider genuinely cannot serve must be rejected at invoke time with the existing typed field error, proven against a provider whose control plane really lacks that capability.
12. Add a direct invocation route to both applications so a flow invocation can be opened from an identifier alone. It is the path an operator follows from a CLI or an alert, and it must reconstruct the same state the session list produces.
13. Independently audit that no provider kind resolves to local Docker except `local_docker` and no test passes by accepting a loopback alias.

## Acceptance tests

- **A10.1 — Shared conformance.** Every provider passes provision, provider-native resource proof, health, authenticated discovery, bash/edit/LSP, timeout/background job, restart or replacement, reconcile, terminate, repeated terminate, and leak detection.
- **A10.2 — Kubernetes BYO.** Against kind, register a kubeconfig after dry run, provision into the configured namespace, inspect pod/service/labels/resources, execute tools, delete the pod and observe reconcile, then terminate with no resources left.
- **A10.3 — Hosted isolation and quota.** Two orgs provision hosted environments. Native resource inspection proves namespace/RBAC/network/quota separation; credentials and endpoints do not cross; exceeding quota returns a typed error; usage is tagged hosted.
- **A10.4 — Nomad BYO.** Against a real Nomad dev agent, inspect registered job/allocation/resources, execute tools, stop the allocation and observe replacement/reconcile, then purge and verify absence.
- **A10.5 — Tunnel local.** Start the shipped env agent behind a real outbound tunnel, provision and execute through the public tunnel endpoint, sever transport and observe suspended state, restore it and resume, then revoke the lease and prove the endpoint/token no longer works.
- **A10.6 — Credentials and tenancy.** Invalid credentials/config fail dry run without persistence; secrets are redacted in events/logs/errors; rotated credentials affect new/reconciled resources as documented; cross-org provider IDs are indistinguishable from missing.
- **A10.7 — Application onboarding.** Web and GPUI register each provider type, display dry-run errors, show health/capabilities/quotas, provision an environment, identify its actual provider, recover from a provider fault, and remove the provider only when ownership rules permit.
- **A10.8 — CI topology.** Required jobs fail if kind, Nomad, tunnel, or provider-native inspection is bypassed. Scheduled jobs cover pinned supported versions and publish conformance artifacts.
- **A10.9 — Documentation and static wiring.** Clean-machine scripts follow each onboarding guide and complete conformance. Static provider configuration is selected by the worker and proven via native resource inspection.
- **A10.10 — Provider capability is behavioral.** *(inherited from Phase 9.)* Register a provider whose control plane genuinely cannot serve environment health checks. A flow declaring `readiness: "health"` against it is rejected at invoke time with the `unsupported_provider_capability` field error, nothing is persisted, and the same flow invoked against a capable provider provisions and reaches ready. The rejection must follow from the registration's real capabilities, not from a hard-coded kind list.
- **A10.11 — Direct invocation route.** *(inherited from Phase 9.)* Web and GPUI open a flow invocation from its identifier alone, without first listing a session's invocations, and render the same state, progress, and topology the list path produces. A cross-org identifier is indistinguishable from a missing one. This is what makes `GetFlowInvocation` client-proven rather than covered only through the list surface.

## Validation commands

```sh
task generate
task verify:codegen
task lint
go test ./envprovider/... -count=1 -timeout 30m
go test ./e2e -run 'TestA5|TestA10' -count=1 -timeout 30m
go test ./e2e -run 'TestA9' -count=1 -timeout 30m
task test:functional
task web:test
cargo test --manifest-path ui/desktop/Cargo.toml
python3 scripts/verify-coverage.py
git diff --check
```

The CI manifests must also run provider conformance in kind, Nomad, and tunnel jobs and archive provider-native resource dumps on failure.

## Exit criteria

- A10.1–A10.9 pass in required CI or scheduled real-cluster jobs where explicitly stated.
- Source and wiring contain no Kubernetes/Nomad/tunnel provider alias to local Docker.
- Provider-native inspection proves creation, isolation, replacement, and deletion for each adapter.
- Every Phase 5 audit bullet is closed by real control-plane, tenancy, failure, onboarding, and application evidence.
- Both Phase 9 open items are closed: provider capability is proven behaviorally (A10.10), and `GetFlowInvocation` has its own client evidence through the direct route (A10.11).
