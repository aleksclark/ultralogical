# AGENTS.md

Ultralogical: a durable-session platform for agentic work — multi-tenant,
event-sourced sessions that humans and agents share. Go backend, ConnectRPC
API, Postgres, React/gpui UIs. Currently at **Phase 6.6** (BYO and hosted environment providers;
versioned flows invoke durable agents into shared sessions).

## Cheatsheet

```sh
task build             # go build ./...
task lint              # buf lint + go vet + golangci-lint
task generate          # regen from protos (commit the output!)
task test              # unit + store + queue tests (needs docker)
task test:functional   # e2e/ acceptance suite (real stack)
task verify:codegen    # fail if gen/ is stale
task dev               # local postgres + ultrad + worker
 task web:build         # typecheck + build React SPA
 task web:test          # Playwright golden on real stack
```

- Go 1.25+ (toolchain auto-downloads), `buf`, `docker`, `task`, node 22
  (`npm ci` in `clients/ts` once, for the TS smoke test).
- Run a single acceptance test: `go test ./e2e/ -run TestA02 -v`

## Iron rules

1. **No mocks of our own components.** Tests run real Postgres, real ultrad,
   real queue. The only permitted fake (Phase 1+) is the scripted LLM server.
2. **Tenancy is structural.** All tenant data access goes through
   `store.Org(id)`. Missing and cross-tenant must be indistinguishable
   (`not found`, same message).
3. **Package layout is law.** Root package = domain types + interfaces only;
   subpackages grouped by dependency (`postgres/`, `http/`, `jobqueue/*`);
   main packages wire deps. See agent_docs/conventions.md.
4. **Protos are the source of truth.** Edit `proto/`, run `task generate`,
   commit generated code in the same change. Schema changes are additive-only.
5. **The event log is the contract.** Per-session gapless seq; NOTIFY is a
   wakeup hint, never a data channel; assert against `Subscribe` in tests.
6. **Seams stay clean.** No river/pgx types past `jobqueue`; handlers depend
   only on root interfaces; new seam impls must pass the conformance suite
   unmodified.
7. **UI stacks are fixed.** The web application uses React + Vite + Tailwind + shadcn/ui in a dark-mode theme. The Rust desktop application uses GPUI in a dark-mode theme. Shared client/state cores may be headless-testable, but first-party UI evidence must exercise the actual shadcn/GPUI application path.
8. **Capability coverage is a merge gate.** Any public/API/UI capability added or changed must be exercised through the real Go functional suite and every supported first-party client (currently web Playwright + Rust desktop). Update `e2e/coverage.json` with existing test files; CI must validate references and run them. A filename or smoke test is not evidence—assert observable behavior, failure paths, replay, and tenancy.
9. **Never claim unimplemented coverage.** Before coding a phase, inventory its acceptance bullets against actual implementation. Mark unbuilt bullets explicitly; do not rename partial work “complete,” map nonexistent tests, or let an omnibus test stand in for capabilities it does not assert.
10. Follow the phase plan (`plan/`) — don't build ahead of the current phase
   or invent stopgaps for unbuilt subsystems.

## Docs index

| Doc | Read when |
|---|---|
| [agent_docs/architecture.md](agent_docs/architecture.md) | touching http, store, eventbus, tenancy, queue, clients/UIs |
| [agent_docs/agent_loop.md](agent_docs/agent_loop.md) | touching fantasy, step jobs, credentials, modelscript |
| [agent_docs/dev_environments.md](agent_docs/dev_environments.md) | touching env lifecycle, MCP, metering |
| [agent_docs/providers.md](agent_docs/providers.md) | touching BYO/hosted provider registration and transport |
| [agent_docs/multiplayer.md](agent_docs/multiplayer.md) | touching presence, grants, run trees, memory |
| [agent_docs/flows.md](agent_docs/flows.md) | touching flow definitions, validation, invocation |
| [agent_docs/phase_6_5_audit.md](agent_docs/phase_6_5_audit.md) | Phase 6 implemented/incomplete capability audit |
| [agent_docs/phase_6_6.md](agent_docs/phase_6_6.md) | Phase 6.6 implemented/deferred status |
| [agent_docs/cross_client_testing.md](agent_docs/cross_client_testing.md) | adding public capabilities or client tests |
| [agent_docs/package_layout.md](agent_docs/package_layout.md) | deciding where new code goes |
| [agent_docs/testing.md](agent_docs/testing.md) | writing/running tests, using the harness |
| [agent_docs/codegen.md](agent_docs/codegen.md) | changing protos, adding events/RPCs |
| [agent_docs/conventions.md](agent_docs/conventions.md) | code style, layout rules, errors, migrations, never-do list |
| [plan/index.md](plan/index.md) | architecture rationale + full roadmap |
| [plan/phase_6_6.md](plan/phase_6_6.md) | current audit-remediation phase |
| [plan/phase_7.md](plan/phase_7.md) | next product phase |
