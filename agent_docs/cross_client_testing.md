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

## Required workflow for every public change

Before implementation:

- Inventory the new/changed capability as user-observable behaviors, including
  success, failure, replay/reconnect, tenancy, and multi-client effects.
- Identify which clients expose it. If a supported client cannot expose it,
  implementation is incomplete—not exempt from testing.
- Add acceptance IDs to the current phase plan and draft the web + Rust test
  names before production code.

During implementation:

- Add/extend the Go real-stack functional test first; assert the event log and
  durable state, not private helpers.
- Add Playwright coverage through the actual dark-mode shadcn web application, visible controls, and rendered output.
- Add Rust desktop coverage through the actual dark-mode GPUI application path. `DesktopClient`/`DesktopState` may be shared underneath it, but tests must drive GPUI actions/state used by the native entrypoint; raw stubs or a print-only shell do not count.
- Add the capability to `e2e/coverage.json` only after both client tests exist.

Before merge:

```sh
python3 scripts/verify-coverage.py
npm run build --prefix ui/web
cargo test --manifest-path ui/desktop/Cargo.toml --no-run
go test ./... -count=1 -timeout 15m
```

Also run the Playwright and Rust real-stack scenarios explicitly when changing
either client. Never skip because the backend test passes.

## Coverage matrix rules

- Keys describe one bounded observable capability, not an entire phase.
- Values reference real files under `ui/web/e2e/` and `ui/desktop/tests/`.
- Do not add unimplemented planned behavior to the matrix. Track it in the
  phase plan as incomplete instead.
- Do not remove a row to make CI pass unless the product capability is removed
  from API, clients, docs, and migration compatibility deliberately.
- When adding a new first-party client, every existing row must gain that
  client's evidence before the client is considered supported.

## Audit requirement at phase boundaries

At the end of each phase, independently compare actual public proto RPCs,
client controls/commands, event variants, and lifecycle states against the
matrix. List uncovered or unimplemented items honestly. The phase cannot be
called complete while implemented functionality lacks real client evidence or
while planned acceptance bullets are silently absent.
