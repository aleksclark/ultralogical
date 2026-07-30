# Phase 3.5 — Cross-client functional completeness

**Goal:** before adding flows, prove every capability delivered in Phases 0–3
through both first-party user experiences: the React web application and the
Rust desktop application. No backend-only acceptance gap remains.

## Coverage matrix

Both clients run against `testkit/harness` (real Postgres, ultrad, worker,
River, Bezalel; modelscript is the only fake) and exercise:

1. Authentication and org/session creation/listing.
2. Durable event replay and reconnect-by-seq.
3. Streaming agent text, tool cards, cancellation, and structured ask-user.
4. Inference credential settings: API key, base URL, extra headers; values
   remain write-only.
5. Local dev environment provision/readiness, real ExecPreview, environment
   MCP tools, persistence, termination, and usage visibility.
6. Participant join/heartbeat/leave and multiplayer convergence.
7. Concurrent run rendering and parent/run attribution.
8. Session-memory set/get/list/delete and cap errors.
9. Cross-tenant denial for sessions, runs, environments, providers, and
   memory.

## Deliverables

- Web Playwright scenarios split by capability, all required in CI.
- `clients/rust`: prost/tonic generated client crate from the same protos.
- `ui/desktop`: a Rust desktop application core with typed state reducer and
  commands for all current capabilities; native-shell smoke plus real-stack
  e2e tests. The application uses the same desktop core the UI entrypoint
  uses — no test-only API path.
- A machine-readable `e2e/coverage.json` mapping each capability to web and
  Rust tests; CI fails if either side is missing.

## Exit criteria

- Every matrix row has a passing web and Rust test against the real stack.
- Protobuf codegen for Go, TypeScript, and Rust is reproducible and drift-
  checked.
- Full API, web, and Rust suites are required checks for merge.
