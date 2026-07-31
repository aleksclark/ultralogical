# Phase 13 — Desktop distribution and cross-application release proof

**Duration:** 2 weeks · **Depends on:** Phase 12

## Goal

Turn the already functional GPUI application into a supported distributable desktop product and prove release-wide parity between Go, web, and desktop. This phase does not postpone basic GPUI implementation: Phases 7–12 must add and test each capability in GPUI when that capability ships.

## Scope

**In:**

- Final Rust client/codegen publishing policy and full public-API conformance.
- Production desktop connection profiles, secure credential storage, update/distribution metadata, crash diagnostics, accessibility/keyboard review, and large-session performance.
- Signed or explicitly documented unsigned macOS bundle, Linux packages/binaries, checksums, SBOMs, provenance, and install/upgrade/uninstall instructions.
- Cross-application multiplayer and reconnect capstones spanning the completed product.
- A generated parity inventory from proto RPCs, event variants, roles, lifecycle states, and product actions.

**Out:** Windows packaging unless separately approved, app-store submission, mobile clients, and enterprise device management.

## Required implementation sequence

1. Generate the release parity inventory. Every public action must map to Go functional evidence, a web action/test, and a GPUI action/test, or be explicitly classified as non-user-facing with justification.
2. Complete Rust client conformance against the real stack, including all services, streaming resume, typed errors, auth refresh, large payloads, and forward-compatible unknown events.
3. Ensure the GPUI entrypoint creates the same application state/actions tested by the suite. Remove or relabel any direct-tonic test that claims UI evidence.
4. Add production connection profiles, OIDC/browser handoff where supported, secure OS credential storage, logout/revocation, certificate/TLS failure handling, and offline/reconnect UX.
5. Test large timelines, concurrent lanes, flow graphs, provider state, schedules, traces, and billing views for bounded memory and responsive interaction.
6. Add keyboard navigation, focus behavior, semantic labels available through GPUI, contrast checks, scaling, and reduced-motion behavior where applicable.
7. Build reproducible release artifacts with generated clients, licenses, SBOM, checksums, source revision, and build provenance. Exercise install, launch, upgrade, rollback, and uninstall on clean CI images.
8. Run a cross-application capstone: browser and desktop users share sessions while flows, cohorts, environments, integration tools, schedules, auth changes, billing limits, replica restarts, and reconnects occur.
9. Publish versioning, compatibility, support, diagnostics, and release procedures. Rehearse rollback against the prior release artifact.
10. Conduct an independent release audit. Any parity row without application-path evidence blocks release.

## Acceptance tests

- **A13.1 — Schema and client parity.** Rust generation is drift-gated and the Rust client exercises every public service against the real stack, including streaming resume, typed authorization/billing errors, unknown additive events, and OIDC token refresh.
- **A13.2 — GPUI application integrity.** Tests launch the real GPUI entrypoint or its exact application builder, open a window/test window, invoke user actions, and inspect rendered/reduced state. A mutation that swaps in a direct RPC-only path fails the evidence gate.
- **A13.3 — Secure connection lifecycle.** Add an OIDC profile, authenticate through the supported handoff, store credentials in OS facilities, restart and reconnect, revoke/logout, reject invalid TLS, and prove tokens do not appear in files, logs, crash data, or UI state.
- **A13.4 — Large-session performance.** Replay and interact with a documented large event log containing concurrent runs, tool cards, flows, environments, and traces. Memory stays bounded and measured interaction latency remains under checked-in regression ceilings.
- **A13.5 — Accessibility and interaction.** Keyboard-only users can reach and activate primary workflows; focus survives live updates; labels and contrast meet the documented baseline; scaling does not hide required actions.
- **A13.6 — Cross-application capstone.** Web and GPUI share one real session and converge on identical event order and durable state while actions originate from both, one ultrad rolls, one worker dies, and both clients resume without gaps or duplicate user-visible entries.
- **A13.7 — Release artifacts.** Clean macOS and Linux jobs verify artifact checksum/SBOM/provenance, install, launch against the harness, complete a session workflow, upgrade from the checked-in compatibility-baseline package built by the release fixture, rollback to that baseline, and uninstall without leaving credentials. After the first public release, the prior published artifact replaces the fixture.
- **A13.8 — Parity and documentation audit.** Generated inventory has no unexplained row; release/support/diagnostics/compatibility docs are executed where possible; package version, protocol compatibility, and source revision agree across artifacts.

## Validation commands

```sh
task generate
task verify:codegen
task lint
task test:functional
task web:test
cargo test --manifest-path clients/rust/Cargo.toml
cargo test --manifest-path ui/desktop/Cargo.toml
python3 scripts/verify-coverage.py
git diff --check
```

Release CI additionally builds and tests clean macOS and Linux artifacts, produces checksums/SBOM/provenance, and runs upgrade/rollback jobs.

## Exit criteria

- A13.1–A13.8 pass in required release CI.
- Every supported capability has real Go, rendered shadcn, and rendered GPUI evidence; the parity inventory has no silent omission.
- Installable artifacts are reproducible, traceable to source, tested on clean systems, and covered by upgrade/rollback procedures.
- The independent release audit has no critical/high finding and no unsupported UI-parity claim.
