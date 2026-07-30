# AGENTS.md

Ultralogical: a durable-session platform for agentic work — multi-tenant,
event-sourced sessions that humans and agents share. Go backend, ConnectRPC
API, Postgres, React/gpui UIs (later phases). Currently at **Phase 0**.

## Cheatsheet

```sh
task build             # go build ./...
task lint              # buf lint + go vet + golangci-lint
task generate          # regen from protos (commit the output!)
task test              # unit + store + queue tests (needs docker)
task test:functional   # e2e/ acceptance suite (real stack)
task verify:codegen    # fail if gen/ is stale
task dev               # local postgres + ultrad
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
3. **Protos are the source of truth.** Edit `proto/`, run `task generate`,
   commit generated code in the same change. Schema changes are additive-only.
4. **The event log is the contract.** Per-session gapless seq; NOTIFY is a
   wakeup hint, never a data channel; assert against `Subscribe` in tests.
5. **Seams stay clean.** No river/pgx types past `jobqueue`; new seam impls
   must pass the conformance suite unmodified.
6. Follow the phase plan (`plan/`) — don't build ahead of the current phase
   or invent stopgaps for unbuilt subsystems.

## Docs index

| Doc | Read when |
|---|---|
| [agent_docs/architecture.md](agent_docs/architecture.md) | touching server, store, eventbus, tenancy, queue |
| [agent_docs/testing.md](agent_docs/testing.md) | writing/running tests, using the harness |
| [agent_docs/codegen.md](agent_docs/codegen.md) | changing protos, adding events/RPCs |
| [agent_docs/conventions.md](agent_docs/conventions.md) | code style, errors, migrations, never-do list |
| [plan/index.md](plan/index.md) | architecture rationale + full roadmap |
| [plan/phase_1.md](plan/phase_1.md) | the next phase to implement |
