# Phase 6.5 — Advanced-loop validation and e2e audit

**Goal:** independently audit every public capability implemented through
Phase 6 and close real functional gaps before production hardening.

## Required audit

1. Inventory actual proto RPCs, event variants, tools, hooks, provider kinds,
   UI controls, GPUI actions/state, and lifecycle states.
2. Compare inventory with `e2e/coverage.json`; remove false claims and add
   missing capability-specific Go, dark-mode shadcn Playwright, and dark-mode
   GPUI/Rust evidence.
3. Exercise compaction, fallback, hook idempotency, integration tools,
   periodic prompts, tracing, restart/replay, tenancy, and secret redaction
   against real stack boundaries.
4. Verify referenced test declarations and execute every suite in CI.

## Exit criteria

- No implemented public capability lacks Go + shadcn web + GPUI desktop
  evidence.
- No planned-but-unbuilt Phase 6 bullet is described as complete.
- Full codegen, lint, Go, Playwright, GPUI/Rust, provider conformance, and
  coverage-validator gates pass.
