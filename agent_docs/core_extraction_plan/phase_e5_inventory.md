# Phase E5 inventory — table → RPC mapping

| Table / subsystem | List/search | Detail | Relationships / notes |
|---|---|---|---|
| `tenants` | `ListTenants` | `GetTenant` | `ListRelated(tenants)` → sessions, api_keys, providers |
| `api_keys` | `ListAPIKeys` | `GetAPIKey` | Never raw key / full hash / key_enc plaintext |
| `sessions` | `ListSessions` | `GetSession` | labels in summary; `ListRelated` → runs, resources, events, memory |
| `session_events` | `ListEvents` | `GetEvent` | payload preview in list; full JSON in detail |
| `session_memory` | `ListMemory` | `GetMemory` | value JSON only in detail |
| `agent_runs` | `ListRuns` | `GetRun` | history via `GetRunHistory` blob; policy/model/result in detail |
| `agent_run_steps` | `ListRunSteps` | (row is detail-sized) | related from run |
| `run_waits` | `ListWaits` | `GetWait` | members included in detail |
| `run_wait_members` | via `GetWait` | — | no independent list (child of wait) |
| `resources` | `ListResources` | `GetResource` | spec/handle in detail; token hash prefix only |
| `provider_instances` | `ListProviders` | `GetProvider` | config/capabilities JSON in detail |
| `credentials` | `ListCredentials` | `GetCredential` | ciphertext length only |
| `periodic_prompts` | `ListPeriodicPrompts` | `GetPeriodicPrompt` | full prompt in detail |
| `hook_cursors` | — | — | Internal progress markers; optional future list if E6 needs a screen |
| `river_job` | `ListJobs` | `GetJob` | requires river migrate; empty if schema absent |
| Runtime health | — | `GetRuntimeHealth` | build/schema/queue depths/counts |
| Session timeline | `GetSessionTimeline` | — | unions events, runs, resources, waits |

## Query foundation evidence

| Behavior | Location |
|---|---|
| Descriptors | `admin/query/descriptors.go` |
| Compile / allowlist | `admin/query/compile.go` |
| Signed cursors | `admin/query/cursor.go` |
| Unit rejection tests | `admin/query/query_test.go` |
| Store reads | `postgres/admin.go` |
| HTTP + auth | `adminhttp/` |
| Binary | `cmd/coreadmin` |
| Functional tests | `admin/admin_test.go` |

## Deferred with justification

| Item | Why |
|---|---|
| `hook_cursors` dedicated list | Internal progress markers; recoverable from session event seq + health |
| Full 100k-event CI seed | 5k bulk path in CI for runtime; same test scales locally |
| mTLS operator auth | Token auth shipped; mTLS/short-lived deployment tokens in E7 |
| Break-glass secret reveal | Explicitly E7 |
