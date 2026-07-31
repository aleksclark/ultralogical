# Conventions

## Package layout

We follow the standard package layout in
[`package_layout.md`](package_layout.md), adapted as follows:

1. **Root package (`ultra`) is for domain types** — types (`Org`, `Session`,
   `Event`, ...) and interfaces (`Store`, `EventBus`, `Authenticator`). It
   depends on no other package in the app. Logic is allowed only when it
   depends solely on domain types (e.g. `DevTokenAuthenticator`).
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
4. **Main packages tie dependencies together** — `cmd/ultrad` (and later
   `cmd/worker`, `cmd/ultra`) do compile-time dependency injection and
   nothing else.

Client libraries live in `clients/<lang>/`; UI applications in `ui/<app>/`;
committed Go codegen in `gen/go/` — see agent_docs/architecture.md
("Clients & UIs"). The web UI must use shadcn/ui components on Tailwind in a
dark-mode theme; do not introduce a competing component system. The Rust
desktop UI must use GPUI in a dark-mode theme; headless state/client code is
shared implementation, not the desktop UI itself.

## Go

- Module `github.com/aleksclark/ultralogical`, root package `ultra`. Domain
  IDs are typed strings (`ultra.OrgID`, `ultra.SessionID`, ...) — UUIDs
  minted with `google/uuid` at creation sites (handlers), not in the store.
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
  migration; add a new one.
- Transactions: `Store.Tx(ctx, fn)`; nested calls reuse the outer tx. Use
  `(*postgres.Store).PgxTx()` to reach the pgx.Tx for `jobqueue.EnqueueTx`.
- Interfaces own their seams: `Store`/`EventBus`/`Authenticator` (root),
  `jobqueue`. Backends/impls never leak types through a seam; new impls must
  pass the seam's conformance suite.
- `log/slog` for logging (JSON in ultrad). No `fmt.Print*`.
- Lint: `golangci-lint` (config `.golangci.yml`) + `go vet` + `buf lint`,
  all via `task lint`.

## Naming & docs

- Files: `snake_case.go`; tests `*_test.go` in the same package for
  white-box store tests, `package x_test` for black-box.
- Every exported symbol has a doc comment; package docs explain the
  package's role in one paragraph and name its seam/contract if it has one.
- Acceptance tests: `TestA<phase><n>_Name` mapping to `plan/phase_*.md`.

## Commits / PRs

- Small, phase-scoped changes. Reference the plan doc + acceptance IDs your
  change advances (e.g. "implements A1.3").
- Never commit with failing `task lint` or `task test`.
- Generated code (`gen/`, `clients/ts/src/gen/`) is committed and must be
  regenerated in the same commit as the proto change.

## Things to never do

- Add an unscoped accessor for tenant data to `Store` (only `SessionOrg`
  directory lookups are allowed, and callers must collapse denials into
  not-found).
- Return different errors for "missing" vs "not yours" (existence oracle).
- Import river/pgx types into handler or domain code across the jobqueue
  seam; import `postgres` from `http` (handlers see only root interfaces).
- Mock the store, the queue, the bus, or ultrad in tests; add a `mock/`
  package.
- Weaken a conformance suite to make an implementation pass.
- Depend on NOTIFY payloads for event content (they are wakeup hints only).
