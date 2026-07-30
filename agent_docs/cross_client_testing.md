# Cross-client functional testing

Phase 3.5 closes the gap between backend acceptance tests and first-party
clients. `e2e/coverage.json` is the required capability matrix: every row
must name both a Playwright test and a Rust desktop test.

Web tests live under `ui/web/e2e` and run the built React application in real
Chromium. Rust uses `clients/rust` (prost/tonic generated from the same proto
sources) and `ui/desktop`; `desktop_e2e.rs` drives the desktop application's
shared state/client core against `testkit/harness`.

Both suites use real Postgres, River, ultrad, worker, and Bezalel. Only the
external LLM vendor is replaced by modelscript. Adding a public capability
requires adding it to the matrix and implementing both client tests before
merge.
