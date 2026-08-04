# Phase E5 — Private admin API and query foundations

**Objective:** Add a private operator API that exposes ultracore's complete
internal state for debugging without expanding, reusing, or weakening the
normal consumer API. Build cursor pagination, filtering, and search once as
composable primitives before any admin SPA screens are added.

**Depends on:** E4.

---

## Scope

- A separate `coreadmin` binary and `proto/admin/v1` API.
- A separate listener, authentication policy, generated clients, and routing
  tree from `cored` and `proto/core/v1`.
- Read-only access to every persisted subsystem plus queue/provider runtime
  diagnostics.
- Shared, bounded query primitives for all admin collections.
- No SPA yet; E6 builds against this contract.

## Separation contract

The admin API is not an elevated mode of the client API:

- `coreadmin` is a separate process and deployment group. `cored` never mounts
  admin routes, even behind a flag.
- Admin protos live under `proto/admin/v1`; no admin messages or RPCs appear in
  `proto/core/v1` or either consumer SDK.
- Admin handlers depend on admin-specific read interfaces. Normal handlers
  continue to depend only on tenant-scoped root interfaces.
- Admin requests do not carry or infer a tenant scope. Tenant is an explicit
  filter and result field so operators can investigate cross-tenant behavior.
- The listener binds to a private address by default, has no public Traefik
  route, and requires operator authentication. Production uses mTLS or a
  short-lived operator token supplied by the deployment secret store.
- CORS is disabled by default and may only name the separately deployed admin
  SPA origin.
- Browser credentials never reuse tenant API keys.

## Query foundation

Every list/search RPC uses the same query envelope:

```proto
message PageRequest {
  uint32 limit = 1;            // default 50, maximum 250
  string cursor = 2;           // opaque, signed, stable keyset cursor
}

message SearchRequest {
  string query = 1;            // escaped text search, never raw SQL
  repeated Filter filters = 2; // typed field/operator/value triples
  repeated Sort sorts = 3;     // stable final tie-breaker is primary key
  PageRequest page = 4;
}

message PageInfo {
  string next_cursor = 1;
  bool has_more = 2;
}
```

Rules:

- Keyset pagination, not offset pagination, for large/mutable tables.
- Cursor includes sort values, primary key, query fingerprint, and expiry; it
  is signed so callers cannot turn it into arbitrary predicates.
- Filters are typed and allowlisted per collection. Unknown fields/operators
  fail with a typed error.
- Search is composable with filters, sorting, and tenant/time ranges.
- Empty search uses indexed ordering; no endpoint may load an unbounded table.
- Responses contain summaries only. Large JSON, logs, prompts, payloads, and
  stack traces use dedicated detail/blob RPCs.
- Exact IDs bypass search and have dedicated lookup RPCs.
- Query plans for common filters are covered by indexes and benchmarked at
  realistic cardinalities.

## Work items

### T5.1 — Admin process and isolation

- Add `cmd/coreadmin`, private HTTP/ConnectRPC listener, `/healthz`, and
  `/readyz`.
- Add separate admin auth middleware and configuration (`CORE_ADMIN_*`).
- Refuse startup when authentication is absent outside an explicit local-dev
  mode.
- Add a Nomad admin group on a private service registration with no Traefik
  tags. Network-policy and deployment tests prove the public cored address
  cannot route an admin RPC.

### T5.2 — Admin proto and generated client

- Add `proto/admin/v1/query.proto` and `admin.proto`.
- Generate a private TypeScript client consumed only by `admin-web`; do not
  export it from `@ultracore/client`.
- Add a compatibility gate for the admin API independently of `core.v1`.

### T5.3 — Composable query engine

- Implement typed collection descriptors: searchable fields, filter
  operators, sortable fields, default order, summary/detail projections.
- Implement signed keyset cursors and deterministic pagination under
  concurrent inserts.
- Add full-text/trigram indexes only where benchmark evidence requires them.
- Add request cost limits: maximum filters/sorts, query length, time range,
  execution timeout, and response bytes.

### T5.4 — Complete read inventory

Expose summary/list, detail, and relationships for:

- tenants and API-key metadata;
- sessions, labels, memory, events, and event payload envelopes;
- runs, run trees, policies, messages, steps, tool calls, awaits, answers, and
  cancellation/failure detail;
- resources, lifecycle state, epochs, specs, handles, endpoint metadata, and
  reconcile history;
- provider registrations, capabilities, probes, adoption/lister diagnostics;
- inference credential metadata and encrypted-at-rest record diagnostics;
- periodic prompts and enqueue history;
- River jobs, attempts, states, scheduling, errors, and worker ownership;
- deployment/runtime health, build version, schema version, queue depth, event
  lag, and active subscribers.

"Complete" means every persisted table and durable state field is mapped in
`phase_e5_inventory.md`. Secret plaintext is never returned by ordinary read
RPCs: hashes, ciphertext metadata, key version, and redaction status are
visible. A separately authorized, audited break-glass reveal path is deferred
until E7.

### T5.5 — Relationship and timeline endpoints

- Session diagnostic timeline combines events, runs, steps, tool calls,
  resources, queue attempts, and actor attribution using stable cursors.
- Relationship RPCs navigate tenant → session → run tree → step/tool call and
  provider → resource → queue/reconcile attempts without client-side joins.
- Raw row/detail payloads retain field names and versions needed to compare
  API projections with database state.

### T5.6 — Tests, inventory, and docs

- Add real-Postgres admin functional tests with at least 100k events, 20k
  runs, and 10k sessions.
- Add `phase_e5_inventory.md` mapping every table/internal subsystem to RPCs.
- Document private deployment, operator auth, query grammar, limits, and
  explicit non-goals in `docs/admin-api.md`.

---

## Acceptance criteria

- **A5.1** No admin service, route, proto, generated symbol, or auth mode is
  reachable from `cored`, `proto/core/v1`, Go SDK, or `@ultracore/client`.
- **A5.2** Public-address tests receive 404 for every admin route; missing,
  expired, or wrong-audience operator credentials fail closed.
- **A5.3** Every persisted table and runtime diagnostic in T5.4 has list,
  detail, search/filter, and relationship evidence or an explicit documented
  reason that only detail applies.
- **A5.4** All collection responses are bounded. Limits over 250 fail; cursors
  are opaque/signed/query-bound; concurrent inserts produce no duplicates or
  skipped rows in a complete traversal.
- **A5.5** Search + filters + sorting compose for every collection descriptor;
  invalid fields/operators cannot reach SQL construction.
- **A5.6** p95 first-page and next-page latency remains under 200 ms on the
  seeded large dataset; no accepted query performs an unbounded sequential
  scan without an audit exception.
- **A5.7** Summary responses stay under configured byte limits; large payloads
  are fetched only through detail/blob RPCs.
- **A5.8** Secret plaintext and tenant API keys never appear in ordinary admin
  responses, errors, traces, or logs.

## Test coverage

| Behavior | Evidence | Tier |
|---|---|---|
| Process/API separation | public-route negative tests + binary dependency check | functional + CI |
| Operator auth | expiry, audience, revocation, mTLS/token tests | functional |
| Cursor correctness | concurrent insert traversal property tests | store + functional |
| Query composition | descriptor conformance suite for every collection | store |
| Full internal inventory | `phase_e5_inventory.md` verifier | CI |
| Large-dataset latency/plans | Postgres benchmarks + `EXPLAIN` assertions | benchmark |
| Secret non-disclosure | response/log redaction tests | security |

## Exit audit

`phase_e5_audit.md` independently walks the schema, queue, provider registry,
and runtime diagnostics; proves no internal data family is missing; verifies
admin/public isolation; and records pagination/search benchmarks. E6 may start
only when the query foundation is stable enough that no screen needs bespoke
list mechanics.
