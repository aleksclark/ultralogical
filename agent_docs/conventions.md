# Conventions

## Package layout

We follow the standard package layout in
[`package_layout.md`](package_layout.md), adapted as follows:

1. **Root package (`core`, import alias `uc`) is for domain types** — types
   (`Org`, `Session`, `Event`, `Grants`, ...) and interfaces (`Store`,
   `EventBus`, `Authenticator`). It depends on no other package in the app.
   Logic is allowed only when it depends solely on domain types (e.g.
   `DevTokenAuthenticator`). Some source filenames are historical
   (`ultra.go`, `multiplayer.go`); the package name is `core`.
2. **Subpackages are grouped by dependency** — `postgres/` owns everything
   pgx/Postgres (Store impl, EventBus impl, migrations); `http/` isolates
   all net/http + ConnectRPC code (the transport adapter);
   `jobqueue/river/` and `jobqueue/inproc/` own their backends.
   Cross-dependency communication happens only through root-package
   interfaces. Small interface-only seam packages (`jobqueue`, like stdlib
   `io`) are allowed when the seam's contract needs types (pgx.Tx) that
   would pollute the root package.
3. **Tenet 3 (shared mock subpackage) is deliberately replaced.** We do not
   mock our own components; the connection points the layout creates are
   exercised by real implementations under conformance suites and the
   real-stack harness instead (see agent_docs/testing.md). Do not add a
   `mock/` package.
4. **Main packages tie dependencies together** — `cmd/cored`,
   `cmd/coreworker`, and `cmd/core` do compile-time dependency injection and
   nothing else.

Client libraries live in `clients/<lang>/` (today: `clients/ts`); committed
Go codegen in `gen/go/core/v1` — see agent_docs/architecture.md. There is no
first-party UI tree after E1; consumers bring UI.

## Go

- Module `github.com/aleksclark/ultracore`, root package `core` (import as
  `uc`). Domain IDs are typed strings (`uc.OrgID`, `uc.SessionID`, ...) —
  UUIDs minted with `google/uuid` at creation sites (handlers), not in the
  store.
- Errors: stores translate backend errors to the sentinels in the root
  package (`ErrNotFound`, `ErrAlreadyExists`, `ErrPermissionDenied`);
  handlers map sentinels to Connect codes in `http/convert.go:mapStoreErr`.
  Internal error details never reach clients.
- Wrap errors with package-prefixed context: `fmt.Errorf("postgres: create
  org: %w", err)`.
- SQL lives inline in store methods (no ORM, no query builder). Every
  tenant-scoped query includes `org_id` — see the OrgScope rule in
  agent_docs/architecture.md.
- Migrations: `postgres/migrations/NNNNN_name.sql`, goose `-- +goose Up` /
  `-- +goose Down`, sequential 5-digit prefixes. Never edit an applied
  migration; add a new one. Product tables left by E1 deletions are dropped
  by the E4 squash, not piecemeal.
- Transactions: `Store.Tx(ctx, fn)`; nested calls reuse the outer tx. Use
  `(*postgres.Store).PgxTx()` to reach the pgx.Tx for `jobqueue.EnqueueTx`.
- Interfaces own their seams: `Store`/`EventBus`/`Authenticator` (root),
  `jobqueue`. Backends/impls never leak types through a seam; new impls must
  pass the seam's conformance suite.
- `log/slog` for logging (JSON in cored). No `fmt.Print*`.
- Lint: `golangci-lint` (config `.golangci.yml`) + `go vet` + `buf lint` +
  extraction fences, all via `task lint`.
- Env prefix is `CORE_*` (`CORE_MASTER_KEY`, `CORE_DEV_TOKENS`,
  `CORE_BEZALEL_IMAGE`, ...).

## Naming & docs

- Files: `snake_case.go`; tests `*_test.go` in the same package for
  white-box store tests, `package x_test` for black-box.
- Every exported symbol has a doc comment; package docs explain the
  package's role in one paragraph and name its seam/contract if it has one.
- Acceptance tests: `TestA<phase><n>_Name` mapping to the owning plan /
  inventory.

## Commits / PRs

- Small, phase-scoped changes. Reference the plan doc + acceptance IDs your
  change advances (e.g. "implements A1.3").
- Never commit with failing `task lint` or `task test`.
- Generated code (`gen/`, `clients/ts/src/gen/`) is committed and must be
  regenerated in the same commit as the proto change.
- Follow the extraction plan; do not build ahead of the current phase.

## Things to never do

- Add an unscoped accessor for tenant data to `Store` (only `SessionOrg`
  directory lookups are allowed, and callers must collapse denials into
  not-found).
- Return different errors for "missing" vs "not yours" (existence oracle).
- Import river/pgx types into handler or domain code across the jobqueue
  seam; import `postgres` from `http` (handlers see only root interfaces).
- Mock the store, the queue, the bus, or cored in tests; add a `mock/`
  package.
- Weaken a conformance suite to make an implementation pass.
- Depend on NOTIFY payloads for event content (they are wakeup hints only).
- Reintroduce deleted product surface (flows, billing, hosted isolation,
  presence, grants lattice, first-party UIs) — fences enforce this.
