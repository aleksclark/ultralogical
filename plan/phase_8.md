# Phase 8 — Rust/gpui UI

**Duration:** 3–4 weeks · **Depends on:** Phase 4 (parallelizable with 5–7)

## Goal

The second UI, proving the UI-agnostic constraint for real: a native rust/gpui app with
feature parity to web v1 (Phases 1–3 scope), built on a rust client generated from the
same protos, verified by its own golden functional suite against the same real backend
stack — and by a cross-UI multiplayer test where web and native share a session.

## Scope

**In:**
- `clients/rust`: crate wrapping tonic/prost-generated stubs from `/proto/ultra/v1`
  (buf-driven generation, committed like go/ts); ergonomic layer (typed event enum,
  auto-resuming subscriber, auth).
- Protocol verification: tonic (gRPC) against connect-go's gRPC support; fallback path
  documented if any RPC misbehaves.
- gpui app (`ui/gpui`): session list, live session view (streamed text, tool cards,
  run status), prompt box, answer-question form, env panel, presence, reconnect.
- Rust client conformance: a ported subset of the testclient suite.
- gpui golden functional suite on the shared harness.
- Cross-UI multiplayer test.
- Distribution: macOS app bundle + linux binary via CI (unsigned OK for v1).

**Out:** flow catalog/invoke UI in gpui (post-v1; web covers it), feature parity with
Phase 4–6 web features (cost display, flow provenance — fast-follow).

## Design details

### Rust client (`clients/rust`)

- Codegen: `buf generate` with `protoc-gen-prost` / `protoc-gen-tonic` plugins, output
  committed under `clients/rust/src/gen/` and covered by the same CI diff gate as
  go/ts. No hand-maintained types.
- Ergonomic layer:
  - `Client::connect(url, credentials)` — channel setup, auth interceptor.
  - `client.subscribe(session_id, from_seq)` → `impl Stream<Item = SessionEvent>` with
    **built-in auto-resume**: on stream error or `ResumeRequired`, transparently
    reconnects from `last_seq + 1`; exposes resume notifications for UI affordances.
  - Typed event enum mirroring the proto oneof (generated code exposes it; the wrapper
    adds convenience matching).
- Transport: tonic ⇄ connect-go's native gRPC. Phase-8-week-1 spike: run the ported
  conformance subset early; if any incompatibility appears (unlikely — connect-go
  serves gRPC natively), fallbacks in order: gRPC-only listener on ultrad, or a
  connect-rust community client. The spike gate is A8.1 running before UI work starts.

### gpui app

- Architecture mirrors the web SPA deliberately: a per-session **event reducer**
  (rust port of the same fold: events → view state). The reducer is a pure crate
  (`ui/gpui/src/state/`), unit-tested against recorded event fixtures **exported from
  the functional suite** — the same sequences the backend actually produces, so the two
  UIs provably interpret one log identically.
- Views: session list window; session view with virtualized timeline (streamed text
  with live updates, collapsible tool cards, run status chips, env panel, presence
  strip); prompt input; awaiting-question inline form.
- Connection lifecycle: background tokio task feeding the reducer via channels; gpui
  re-renders on state change; disconnect banner + auto-resume (client layer does the
  work).

### gpui golden suite

- Uses gpui's test context (the Zed-style headless/test harness) driving the real
  app-state layer against a real `testkit/harness` backend (harness gets a small rust
  FFI-free control shim: it's just HTTP — the rust tests call the harness's control
  endpoints to seed modelscript scripts, same as Playwright does).
- Where full visual-tree assertions are impractical in gpui's test harness, the suite
  asserts on the reducer state + targeted element queries — the reducer fixtures cover
  interpretation fidelity, the golden suite covers wiring and interaction.

### Cross-UI multiplayer

The capstone test for constraint 7: one session, one browser (Playwright), one gpui
test instance, both live. Human A (web) prompts; human B (gpui) sees the stream; agent
asks a question; B answers from native; A sees the answer and completion. Event order
identical on both.

## Work breakdown

0. **Define and implement capability evidence first.** Inventory every public behavior this phase will add/change (success, failure, replay/reconnect, tenancy, and distributed failure where applicable). Create capability-specific Go real-stack, Playwright web, and Rust desktop tests using shipped client/application paths. Add `e2e/coverage.json` references only after those existing tests assert observable outcomes. Smoke tests, control presence, unasserted RPCs, test-only shortcuts, and nonexistent filenames are not evidence.

1. Rust codegen pipeline + CI diff gate.
2. Transport spike + ported conformance subset (**gate: A8.1 before UI work**).
3. Client ergonomic layer (auto-resume subscriber, auth).
4. Event reducer crate + fixture round-trip tests (fixtures exported from functional
   suite).
5. gpui shell: session list, session view, prompt/answer, env panel, presence.
6. Streaming rendering + reconnect UX.
7. gpui golden suite on the harness.
8. Cross-UI multiplayer test.
9. CI: rust build/test legs, app bundle artifacts.

## Acceptance tests

- **A8.1 — Client parity from one schema.** The rust client passes a ported subset of
  the Phase 0–3 testclient suite against the real stack: create/get session,
  append/subscribe/resume (A0.2/A0.3 semantics), StartRun with streaming deltas
  (A1.5 semantics), awaiting + PromptRun (A1.3), presence events (A3.1 subset). Runs
  as a required CI leg from the moment it first passes.
- **A8.2 — Reducer fidelity.** Event-sequence fixtures exported from the Go functional
  suite (happy path, awaiting, cancellation, env lifecycle, multi-run interleave) are
  folded by the rust reducer and the TS reducer; both produce equivalent canonical
  view-state JSON (schema-normalized). Divergence fails CI — this is the anti-drift
  gate between UIs.
- **A8.3 — gpui golden.** Native app against the real stack: open session list →
  create session → prompt → observe incremental streamed text (assert >1 intermediate
  reducer state) → agent question rendered → answer inline → run completes → force
  disconnect (harness kills the connection) → auto-resume; final timeline complete and
  gap-free.
- **A8.4 — Cross-UI multiplayer.** Web (Playwright) + gpui instance in one session:
  both render the same event order; presence shows both participants on both UIs; an
  ExecPreview run from web appears in gpui; an answer submitted from gpui unblocks the
  run web is watching.
- **A8.5 — Distribution smoke.** CI produces a macOS bundle and linux binary; the
  linux binary launches headless-smoke (connects to harness, subscribes, exits 0) in
  CI.

## Exit criteria

- **Capability-completeness audit passes:** compare this plan with actual proto RPCs, event variants, UI controls, desktop commands, and lifecycle states. Every implemented capability has capability-specific Go real-stack + Playwright + Rust desktop evidence and a truthful `e2e/coverage.json` entry whose files exist and run in required CI.
- Planned-but-unbuilt acceptance bullets remain explicitly incomplete; they are never silently omitted, renamed as complete, or represented by broader tests that do not assert them. `python3 scripts/verify-coverage.py` and all referenced suites pass.

- A8.1–A8.5 green; rust legs required for merge.
- `clients/rust` published (crates.io or git tag policy decided and documented).
- Parity checklist (`docs/ui-parity.md`) enumerating web-v1 features with gpui status —
  all Phase 1–3 rows checked.
