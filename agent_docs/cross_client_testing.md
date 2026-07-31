# Cross-client functional testing

This is a **merge contract**, not documentation theater. `e2e/coverage.json`
contains only capabilities that are both implemented and proven through every
supported first-party client (currently React/Playwright and Rust desktop).

## What counts as capability evidence

A coverage entry is valid only when its referenced test:

1. exists and is executed by required CI;
2. drives the same application/client code shipped to users, never a test-only shortcut;
3. talks to `testkit/harness` with real Postgres, River, ultrad, worker, and Bezalel (modelscript may replace only the external LLM vendor);
4. performs the public action and asserts its observable result (typed event order/payload, rendered state, persisted/replayed state, terminal state, or uniform denial);
5. covers material failure/authorization paths where the capability has them.

Merely rendering a control, compiling a client, calling an RPC without
asserting results, pointing several rows at one omnibus smoke test, or naming a
nonexistent file **does not count**.

`scripts/verify-coverage.py` enforces this mechanically. Each row declares an
`asserts` list, and verification fails when:

- the referenced file or test does not exist;
- a declared assertion string is absent from the referenced test body;
- no Go functional test launches the referenced client suite, or the Go test
  that does is not executed by required CI;
- a Rust row does not open the GPUI window (`gpui::test` + `open_app`) or never
  inspects a rendered frame (`await_rendered`/`debug_bounds`);
- one named scenario backs more than three capabilities.

`scripts/mutate-coverage-gate.sh` proves each of those rejections still works by
introducing them deliberately and restoring the tree; it runs in required CI.

## Required workflow for every public change

Before implementation:

- Inventory the new/changed capability as user-observable behaviors, including
  success, failure, replay/reconnect, tenancy, and multi-client effects.
- Identify which clients expose it. If a supported client cannot expose it,
  implementation is incomplete—not exempt from testing.
- Add acceptance IDs to the owning completion phase and draft the web + GPUI
  application test names before production code. The ownership map is
  `agent_docs/phases_0_6_audit.md`; do not move a hard behavior into a later
  phase merely because groundwork exists.

During implementation:

- Add/extend the Go real-stack functional test first; assert the event log and
  durable state, not private helpers.
- Add Playwright coverage through the actual dark-mode shadcn web application, visible controls, and rendered output.
- Add Rust desktop coverage through the actual dark-mode GPUI application path. `DesktopClient`/`DesktopState` may be shared underneath it, but tests must drive GPUI actions/state used by the native entrypoint; raw stubs or a print-only shell do not count.
- Add the capability to `e2e/coverage.json` only after both client tests exist.

Before merge:

```sh
python3 scripts/verify-coverage.py
bash scripts/mutate-coverage-gate.sh
npm run build --prefix ui/web
cargo test --manifest-path ui/desktop/Cargo.toml --no-run
go test ./... -count=1 -timeout 30m
go test ./e2e/ -count=1 -timeout 45m
```

Also run the Playwright and Rust real-stack scenarios explicitly when changing
either client. Never skip because the backend test passes.

## Coverage matrix rules

- Keys describe one bounded observable capability, not an entire phase.
- Values reference real files under `ui/web/e2e/` and `ui/desktop/tests/`, and
  name the assertion strings inside them that prove the capability.
- Do not add unimplemented planned behavior to the matrix. Track it in the
  phase plan as incomplete instead.
- Do not remove a row to make CI pass unless the product capability is removed
  from API, clients, docs, and migration compatibility deliberately.
- When adding a new first-party client, every existing row must gain that
  client's evidence before the client is considered supported.

## Audit requirement at phase boundaries

At the end of each phase, an independent reviewer compares the owning plan and
inherited audit rows with actual proto RPCs, client actions, event variants,
lifecycle states, database invariants, CI jobs, and the matrix. List uncovered
or unimplemented items honestly. The phase cannot be called complete while any
scoped bullet is open, implemented functionality lacks real client evidence,
or an acceptance claim is represented only by groundwork, CRUD, an adapter
alias, compilation, or a smoke test.
