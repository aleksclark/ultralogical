# Client functional testing (post-E1)

This is a **merge contract**, not documentation theater. `e2e/coverage.json`
contains only capabilities that are both implemented and proven through the
real Go functional suite. After E1 there is no first-party web or desktop UI;
client evidence is Go functional tests plus (from E4) Go/TS SDK smoke tests.

## What counts as capability evidence

A coverage entry is valid only when its referenced test:

1. exists and is executed by required CI;
2. drives the public API the same way a consumer would (generated Connect
   clients via `testkit/testclient` or `clients/ts`);
3. talks to `testkit/harness` with real Postgres, River, cored, coreworker, and
   Bezalel (modelscript may replace only the external LLM vendor);
4. performs the public action and asserts its observable result (typed event
   order/payload, persisted/replayed state, terminal state, or uniform denial);
5. covers material failure/authorization paths where the capability has them.

Merely compiling a client, calling an RPC without asserting results, pointing
several rows at one omnibus smoke test, or naming a nonexistent file **does
not count**.

`scripts/verify-coverage.py` enforces this mechanically. Each row declares an
`asserts` list under a `go` evidence object, and verification fails when:

- the referenced file or test does not exist;
- a declared assertion string is absent from the referenced test body;
- the Go test is not executed by required CI;
- one named scenario backs more than three capabilities;
- a published RPC is neither claimed by a capability nor listed under
  `deferred`, which makes deleting a row as loud as fabricating one;
- a `deferred` entry names an owner that no phase plan declares as an
  acceptance test.

`scripts/mutate-coverage-gate.sh` proves each of those rejections still works by
introducing them deliberately and restoring the tree; it runs in required CI.
Those CI jobs are required status checks on the default branch, and
`TestA79_RequiredChecksAreEnforced` fails if that stops being true.

## Required workflow for every public change

Before implementation:

- Inventory the new/changed capability as user-observable behaviors, including
  success, failure, replay/reconnect, and tenancy effects.
- Add acceptance IDs to the owning extraction phase inventory and draft the Go
  functional test name before production code.

During implementation:

- Add/extend the Go real-stack functional test; assert the event log and
  durable state, not private helpers.
- When the TS client surface is touched, keep `e2e/ts_smoke_test.go` green.
- Add the capability to `e2e/coverage.json` only after the Go evidence exists.

Before merge:

```sh
python3 scripts/verify-coverage.py
bash scripts/mutate-coverage-gate.sh
bash scripts/check-extraction-fences.sh
go test ./... -count=1 -timeout 30m
go test ./e2e/ -count=1 -timeout 45m
```

## Coverage matrix rules

- Keys describe one bounded observable capability, not an entire phase.
- Values reference real files under `e2e/` with a `go` object naming
  `file`, `test`, and `asserts`. There are no `web` or `desktop` columns.
- `deferred` is for published RPCs deliberately postponed with an owning
  phase; empty is fine when everything is covered.
