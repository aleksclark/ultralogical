# Protos & codegen

The client API is schema-first: protos in `proto/ultra/v1/` are the single
source of truth; generated code is committed and CI fails on drift.

## Toolchain

- `buf` v2 config: `buf.yaml` (module = `proto/`, STANDARD lint, FILE
  breaking) + `buf.gen.yaml` (remote plugins, `clean: true`).
- Outputs (committed):
  - `gen/go/ultra/v1/` — protobuf-go + connect-go (server handlers + client)
  - `clients/ts/src/gen/ultra/v1/` — protobuf-es v2 (`target=ts`; service
    descriptors are in `*_pb.ts`, used with `createClient` from
    `@connectrpc/connect` — there is no separate `_connect.ts` in es v2)
  - `clients/rust/src/gen/` — tonic/prost output used by the GPUI application;
    full release conformance must be completed in Phase 13.
- Keep protos tonic-compatible with no Connect-only extensions.

## Workflow

```sh
# edit proto/ultra/v1/*.proto, then:
task generate          # buf generate (regenerates all configured clients)
task lint              # includes buf lint
go build ./...         # compile against regenerated go
```

Commit the regenerated files with the proto change — the CI codegen-diff
gate (`buf generate` followed by a diff of every configured generated output)
fails otherwise. Phase 7 adds a deliberate stale-output mutation test so a
misconfigured gate cannot silently pass. `buf breaking` runs against the PR base branch: schema evolution
must be additive (new fields, new oneof variants, new RPCs; never renumber,
remove, or retype).

## API design rules (from plan/index.md §2.6)

- Every mutation response carries the event seq it produced (when it
  produces one), so clients can await consistency by subscribing past it.
- `SessionEvent.payload` is a oneof of typed messages — never
  `google.protobuf.Struct` grab-bags in the public API.
- Streaming responses may be event-less keepalives; document such frames in
  the proto comments and make clients skip them.
- RPC response messages are named `<RpcName>Response` (buf lint enforces).

## Adding an event variant (the common change)

1. Add the payload message + oneof field (next free number) in
   `proto/ultra/v1/event.proto`.
2. `task generate`.
3. Add the kind constant in `event.go` (root package).
4. Add both directions in `http/convert.go` (`payloadToDomain`,
   `payloadFromDomain`).
5. Extend `testkit/testclient` helpers if tests need to produce/assert it.
6. Functional test asserting it round-trips through Append/Subscribe.

## Adding an RPC/service

1. Define in proto; `task generate`.
2. Implement the handler in `http/` (thin: authenticate → resolve org →
   check membership → call store → convert). Handlers depend only on root
   interfaces (`ultra.Store`, `ultra.EventBus`, `ultra.Authenticator`).
   Register it in `http.NewHandler` — unary services take the auth
   interceptor; streaming RPCs must authenticate inside the handler.
3. Wire testclient support + functional coverage, including the
   cross-tenant denial case (same not-found as missing).
