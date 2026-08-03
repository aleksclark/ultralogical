# Phase E5 — Floor proof: primer-agent migration

**Objective:** Replace primer-server's bespoke agent substrate
(`agent_sessions` / `agent_messages` / `agent_events` tables, River runner,
SSE plumbing) with the core, keeping primer's product surface (REST +
ConnectRPC APIs, admin SPA, student policy, task automation) intact. This
proves the core's **floor**: resource-free sessions, labels, Actor,
RunPolicy, periodic prompts, and the embed-next-to-an-existing-app story.

**Depends on:** E4 (`v0.1.0`).
**Duration guess:** 1.5–2 weeks, in the primer repo
(`~/work/projects/primer`), with core fixes flowing back as patch releases.

**Rule for this phase:** when primer needs something the core lacks, the
gap is recorded in `phase_e5_gaps.md` and fixed **in the core with its own
coverage** (additive-only), never worked around with a primer-side shadow
table. The migration is the acceptance test of the core (D9).

---

## Scope

- primer-server embeds the Go SDK; core (`cored`+`coreworker`) deploys as a
  sidecar stack next to primer-server (Nomad, same Postgres instance,
  separate database).
- Deleted from primer: migration `00003_agent.sql` tables, `agent.Runner`'s
  loop/persistence, event polling internals. **Kept** in primer: its REST +
  ConnectRPC agent API shapes (now facades over the SDK), the admin SPA,
  `StudentToolPolicy` intent (re-expressed as RunPolicy), grade-tampering
  prompt heuristic, Switchboard-with-primer-plugin tool wiring.

## Mapping

| primer today | on the core |
|---|---|
| `agent_sessions` (kind: admin/student/task) | core sessions, labels: `app=primer, kind=admin|student|task, student=<id>` |
| `agent_messages` | run history envelopes (core-owned) |
| `agent_events` + SSE/poll | `EventService.Subscribe` via SDK; primer's SSE endpoint becomes a translation layer |
| River `agent_run` job per turn | `RunService.StartRun` / answer-awaiting per turn |
| `agent_tasks` + `agent_task_tick` | core periodic prompts on a `kind=task` session; task CRUD stays primer-side (prompt text, enable flag) mapped onto `PeriodicPromptStore` via API |
| `StudentToolPolicy` allow/deny | `RunPolicy{AllowTools: [primer_list_*, primer_get_*, search], DenyTools: [execute, ...], ResourceKinds: [], MaxChildren: 0}` |
| `OPENROUTER_API_KEY` env | tenant credential `openrouter/default` (core gains an openrouter-compatible provider entry in ModelConfig if not already representable — likely gap G1) |
| Switchboard MCP tools | **likely gap G2:** core loop must accept consumer-supplied MCP tool servers *not* tied to a resource (primer's Switchboard is a static sidecar). Expected core addition: tenant- or run-scoped static MCP tool source, conformance-tested |
| student_id attribution | Actor `{kind: "student", id: ...}` |

## Work items

- **T5.1** Deploy core stack alongside primer (Nomad job, separate DB in
  the shared Patroni cluster); create tenant `primer`, keys, credential.
- **T5.2** Gap resolution in core (expected: G1 openrouter model provider,
  G2 static MCP tool sources; others recorded as found). Each lands as a
  core patch release with conformance/functional coverage before primer
  consumes it.
- **T5.3** primer-server: swap `agent.Runner` internals for SDK calls
  behind the existing REST/ConnectRPC handlers; SSE endpoint re-implemented
  over `Subscribe`. Feature flag `AGENT_BACKEND=core|legacy` for the
  transition.
- **T5.4** Data migration: closed historical sessions exported to core
  sessions (events synthesized from messages) or archived read-only in
  primer — decide by volume; record the decision. Active sessions: cut over
  at a quiet moment (single student…).
- **T5.5** Delete legacy: migration 00003 tables dropped (new primer
  migration), runner internals removed, `AGENT_BACKEND` flag removed after
  soak.
- **T5.6** Soak: two weeks of real use (admin chat, student chat, scheduled
  tasks) with core OTLP traces flowing to SigNoz.

---

## Acceptance criteria

- **A5.1** primer's own suites pass on the core backend: `make test`
  (testcontainer suite) green with the agent tests re-pointed; SPA agent
  chat + tasks work end-to-end (manual walkthrough recorded; Playwright
  if primer grows it).
- **A5.2** Student sandbox holds on the core: a student session cannot call
  write tools, `execute`, provision resources, or spawn agents — asserted
  via primer integration tests driving the real core, including the
  uniform-refusal (no existence leak) property.
- **A5.3** Task automation: a scheduled task fires via core periodic
  prompts, produces a run, streams events, and its output is visible in the
  primer admin UI. "Run now" works (immediate run on the task session).
- **A5.4** Streaming parity: admin chat token deltas arrive over primer's
  SSE facade with latency comparable to legacy (measured, recorded); a
  browser refresh mid-run resumes from seq without message loss.
- **A5.5** Labels in anger: primer lists "all of Jacob's student sessions"
  via label selector through the SDK; the admin UI session list is backed
  by it.
- **A5.6** Zero shadow persistence: primer's schema retains no message/
  event/session tables (grep the migrations); every gap fixed core-side is
  listed in `phase_e5_gaps.md` with its core release + test reference.
- **A5.7** Legacy deleted and soak clean: two weeks with no P1 defects
  attributable to the migration; flag removed.

## Test coverage

| Behavior | Test | Where |
|---|---|---|
| primer agent API on core backend | primer integration suite (testcontainer + real core stack) | primer repo |
| Student policy enforcement | primer integration tests + core `e2e/policy_test.go` (G-gap cases) | both |
| Periodic tasks end-to-end | primer integration test | primer repo |
| SSE facade resume | primer integration test | primer repo |
| Label selectors in UI path | primer integration test | primer repo |
| Gap features (G1, G2, …) | core functional + conformance suites | core repo |
| Soak | OTLP dashboards + defect log in audit | ops |

## Exit audit

`phase_e5_audit.md` (lives in the core plan dir, references primer
commits/PRs): A5.1–A5.7 with evidence; the gap ledger; a candid "what the
core got wrong for embedders" list feeding E6 and the backlog.
