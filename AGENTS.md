# AGENTS.md

ultracore: a durable-session substrate for agentic work — multi-tenant,
event-sourced sessions with an owned agent loop and pluggable per-tenant
resource providers. Go backend, ConnectRPC API, Postgres. Consumers bring
their own UI, identity, triggers, and policy.

## Cheatsheet

```sh
task build             # go build ./...
task lint              # buf lint + go vet + golangci-lint + extraction fences
task generate          # regen from protos (commit the output!)
task test              # unit + store + queue + provider conformance (docker)
task test:functional   # e2e/ acceptance suite (real stack)
task cli:test          # cmd/core CLI against the real stack
task verify:codegen    # fail if generated output is stale
task verify:coverage   # capability coverage matrix
task dev               # one-command stack: pg + model + cored + coreworker
task dev:smoke         # boot, smoke, tear down with leak checks
```

- Go 1.25+ (toolchain auto-downloads), `buf`, `docker`, `task`, node 22
  (`npm ci` in `clients/ts` once, for the TS smoke test).
- Run a single acceptance test: `go test ./e2e/ -run TestA02 -v`

## Iron rules

1. **No mocks of our own components.** Tests run real Postgres, real cored,
   real queue. The only permitted fake is the scripted LLM server.
2. **Tenancy is structural.** All tenant data access goes through
   `store.Org(id)` (renames to `Tenant` in E3). Missing and cross-tenant must
   be indistinguishable (`not found`, same message).
3. **Package layout is law.** Root package = domain types + interfaces only;
   subpackages grouped by dependency (`postgres/`, `http/`, `jobqueue/*`);
   main packages wire deps. See agent_docs/conventions.md.
4. **Protos are the source of truth.** Edit `proto/`, run `task generate`,
   commit generated code in the same change. Schema changes are additive-only
   after the E4 baseline reset.
5. **The event log is the contract.** Per-session gapless seq; NOTIFY is a
   wakeup hint, never a data channel; assert against `Subscribe` in tests.
6. **Seams stay clean.** No river/pgx types past `jobqueue`; handlers depend
   only on root interfaces; new seam impls must pass the conformance suite
   unmodified.
7. **Client evidence is the Go functional suite + SDK smoke.** There is no
   first-party web or desktop UI. Public capabilities are proven through
   `e2e/` Go tests and (from E4) Go/TS SDK smoke tests.
8. **Capability coverage is a merge gate.** Any public/API capability added or
   changed must be exercised through the real Go functional suite. Update
   `e2e/coverage.json` with existing test files; CI must validate references
   and run them. A filename or smoke test is not evidence—assert observable
   behavior, failure paths, replay, and tenancy.
9. **Never claim unimplemented coverage.** Before coding a phase, inventory its
   acceptance bullets against actual implementation. Mark unbuilt bullets
   explicitly; do not rename partial work "complete," map nonexistent tests,
   or let an omnibus test stand in for capabilities it does not assert.
10. Follow the extraction plan (`agent_docs/core_extraction_plan/`) — don't
   build ahead of the current phase or invent stopgaps for unbuilt subsystems.
   A phase closes only when every scoped acceptance test and its independent
   completion audit pass.

## Docs index

| Doc | Read when |
|---|---|
| [agent_docs/architecture.md](agent_docs/architecture.md) | touching http, store, eventbus, tenancy, queue |
| [agent_docs/agent_loop.md](agent_docs/agent_loop.md) | touching fantasy, step jobs, credentials, modelscript |
| [agent_docs/dev_environments.md](agent_docs/dev_environments.md) | touching env lifecycle, MCP |
| [agent_docs/providers.md](agent_docs/providers.md) | touching provider registration and transport |
| [agent_docs/core_extraction_plan/index.md](agent_docs/core_extraction_plan/index.md) | extraction roadmap and iron rules |
| [docs/security.md](docs/security.md) | tool allowlists, denial visibility, tenancy, credential scope |
| [agent_docs/package_layout.md](agent_docs/package_layout.md) | deciding where new code goes |
| [agent_docs/testing.md](agent_docs/testing.md) | writing/running tests, using the harness |
| [agent_docs/codegen.md](agent_docs/codegen.md) | changing protos, adding events/RPCs |
| [agent_docs/conventions.md](agent_docs/conventions.md) | code style, layout rules, errors, migrations, never-do list |
