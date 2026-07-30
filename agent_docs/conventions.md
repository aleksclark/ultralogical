# Conventions

## Go

- Module `github.com/aleksclark/ultralogical`, root package `ultra`. Ben
  Johnson layout: root = domain types + interfaces (no I/O); subpackages
  implement (`postgres/`, `jobqueue/river/`, `server/`, ...).
- Domain IDs are typed strings (`ultra.OrgID`, `ultra.SessionID`, ...) —
  UUIDs minted with `google/uuid` at creation sites (handlers), not in the
  store.
- Errors: stores translate backend errors to the sentinels in `errors.go`
  (`ErrNotFound`, `ErrAlreadyExists`, `ErrPermissionDenied`); handlers map
  sentinels to Connect codes in `server/convert.go:mapStoreErr`. Internal
  error details never reach clients.
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
- Interfaces own their seams: `jobqueue`, `server.Authenticator`,
  `eventbus`. Backends/impls never leak types through a seam; new impls must
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
  seam.
- Mock the store, the queue, the bus, or ultrad in tests.
- Weaken a conformance suite to make an implementation pass.
- Depend on NOTIFY payloads for event content (they are wakeup hints only).
