# Phase E3 — Tenancy, identity, labels, and policy hook

**Objective:** Reshape the who-layer for service consumers: Org →
**Tenant** (structural scoping kept verbatim), human users/roles → tenant
**API keys** + opaque **Actor** attribution, session **labels** for
consumer-defined taxonomy, and a **policy hook** that lets a consumer
constrain each run without the core knowing why.

**Depends on:** E2.
**Duration guess:** 1.5–2 weeks.

---

## Scope

The four items above plus credentials cleanup. Nothing else — no quota, no
rate limiting, no admin UI (consumers manage tenants via CLI/API).

## Design

### Tenant (was Org)

```go
type TenantID string
type Tenant struct {
    ID        TenantID
    Name      string
    CreatedAt time.Time
}
// store.Org(id) → store.Tenant(id); OrgScope → TenantScope.
```

- One consumer service (primer, curri, hermes) ≈ one tenant; a consumer may
  hold several (curri prod / curri staging). The core doesn't care.
- Iron rule 2 unchanged: all tenant data through `store.Tenant(id)`; missing
  and cross-tenant indistinguishable.
- Provider instances, credentials, sessions, resources, runs: all already
  tenant-keyed via E1/E2; this is the rename plus the human-model deletion.

### Identity: API keys + Actor

- Human `User`/`OrgMember`/`OrgRole` are deleted. A tenant holds N **API
  keys** (hashed at rest like resource tokens: SHA-256 + AES-GCM, secret
  redactor registered), created/revoked via CLI + API. A key authenticates
  as its tenant; scoping is per-key: `admin` (tenant management) or
  `sessions` (session/run/resource surface).
- **Actor** is opaque attribution the consumer sends per call:
  `{"kind": "...", "id": "...", "display": "..."}` (e.g.
  `student/jacob`, `flow-trigger/slack:C123`). Stored on events, runs, and
  memory writes. The core never interprets it — it exists so a consumer can
  answer "who did this" from the event log.
- `auth.go` reduces to: key lookup → tenant scope + key scope; ConnectRPC
  interceptor attaches `(TenantID, KeyScope, Actor)` to context.

### Session labels

```go
type Session struct {
    ID        SessionID
    TenantID  TenantID
    Title     string
    Labels    map[string]string   // consumer-defined, ≤16 pairs, k/v ≤128 chars
    CreatedAt time.Time
    ArchivedAt *time.Time
}
```

- Indexed (GIN on jsonb or a side table — implementer's choice, benchmarked
  in T3.3). `ListSessions` gains label selectors: equality and set
  membership (`student=jacob`, `subject in (math,ela)`), k8s-selector
  subset. No inequality/regex (YAGNI).
- Labels are mutable via `UpdateSessionLabels`, emitting a
  `SessionLabelsChanged` event (the log remains the source of truth for
  when taxonomy changed).

### Policy hook

The consumer, not the core, knows that a student session must never see
write tools. The core exposes one enforcement point:

```go
// RunPolicy is fixed at run creation and immutable thereafter.
type RunPolicy struct {
    AllowTools []string          // "*" or explicit; replaces E1's interim field
    DenyTools  []string          // evaluated after allow; wins
    ResourceKinds []ResourceKind // kinds this run may provision; empty = none, ["*"] = all
    MaxChildren   int            // spawn cap; 0 = no spawning
    ChildInherit  bool           // children get this same policy verbatim
}
```

- Enforced at tool dispatch (`loop/step.go`) and at
  `provision_resource`/`spawn_agent` execution. Denials produce the uniform
  refusal event (existence-oracle defense retained from E1).
- Children: if `ChildInherit`, child policy = parent policy; otherwise the
  spawning tool call may pass a child policy, which the core validates as a
  **subset** (allow ⊆ allow minus deny, kinds ⊆ kinds, MaxChildren ≤
  parent's−used). This is the useful residue of the old lattice, three
  fields instead of a general structure.
- Prompt-level heuristics (primer's grade-tampering short-circuit) stay
  consumer-side; the core's hook is mechanical.

### Credentials

- Inference credentials stay tenant-scoped and name-addressed
  (`ModelConfig.Credential`), encrypted at rest, redactor-registered —
  unchanged. Credential rotation e2e survives as-is.

## Work items

- **T3.1** Rename Org→Tenant through root types, store, postgres, protos
  (stub level), http, CLI, testkit, docs. Fence: `OrgID`, `OrgScope`,
  `OrgRole`, `OrgMember`.
- **T3.2** Delete human identity; implement API keys (store, hashing,
  CLI verbs `core tenant create|key create|key revoke`, interceptor);
  implement Actor plumbing onto events/runs/memory writes.
- **T3.3** Labels: schema, store methods, selector parsing, list API,
  `SessionLabelsChanged` event. Benchmark selector queries at 10k sessions
  ×8 labels (devstack seed script); record numbers in the audit.
- **T3.4** RunPolicy: type, storage on runs, dispatch enforcement, subset
  validation on spawn, refusal events. Remove E1's interim flat allowlist
  (absorbed here).
- **T3.5** e2e: new `e2e/tenancy_test.go` (key scopes, cross-tenant
  invisibility incl. label queries), `e2e/labels_test.go`,
  `e2e/policy_test.go`; update `agent_test.go` spawn assertions.
- **T3.6** Docs: `docs/security.md` rewritten around keys/Actor/policy;
  `AGENTS.md` iron rule 2 reworded to Tenant.

---

## Acceptance criteria

- **A3.1** Full suite green; fences (E1+E2+E3 terms) pass.
- **A3.2** Cross-tenant invisibility holds for every surface incl. new
  ones: sessions, resources, runs, credentials, provider instances, label
  queries, event subscribe. Same "not found" for missing vs cross-tenant.
  A label selector matching another tenant's sessions returns empty, not
  error.
- **A3.3** Key lifecycle: a revoked key fails closed mid-stream (an open
  Subscribe with a revoked key terminates); `sessions`-scope key cannot
  create tenants or keys; raw keys never appear in logs/events (redactor
  test).
- **A3.4** Actor attribution: every event caused by an API call carries the
  caller's Actor; loop-internal events carry the run's Actor; replay shows
  attribution end-to-end.
- **A3.5** Labels: CRUD + selectors behave per spec; 10k-session selector
  benchmark recorded and < 50ms p95 on devstack hardware; label change
  events appear in replay.
- **A3.6** Policy: denied tool → uniform refusal (no existence leak);
  resource-kind restriction blocks `provision_resource` at execution;
  MaxChildren enforced across cohorts; child policy escaping parent subset
  is refused at spawn with a typed error; `ChildInherit` propagates
  verbatim. Grants-era e2e assertions from E1's interim allowlist are
  superseded and removed.
- **A3.7** Per-tenant provider isolation re-proven under new names: two
  tenants register `byo_k8s` providers pointing at distinct clusters
  (kind/k3d in CI as today); each tenant's resources land only in its own
  cluster (lister evidence both sides).

## Test coverage

| Behavior | Test | Tier |
|---|---|---|
| Tenant scoping incl. labels + events | `e2e/tenancy_test.go` | functional |
| Key scopes, revocation mid-stream, redaction | `e2e/tenancy_test.go` + `secrets` unit | functional + unit |
| Actor on events/runs/memory | `e2e/tenancy_test.go` replay assertions | functional |
| Label CRUD/selectors/events | `e2e/labels_test.go` + store tests | functional + store |
| Selector performance @10k | benchmark in `postgres/` (recorded, not gating CI) | bench |
| Policy enforcement (tools/kinds/spawn/subset/inherit) | `e2e/policy_test.go` + `loop` unit tests | functional + unit |
| Two-tenant provider isolation | `e2e/provider_test.go` (extended) | functional |
| Store conformance for new stores (keys, labels) | `postgres/store_test.go` | store |

## Exit audit

`phase_e3_audit.md`: A3.1–A3.7 with evidence; explicit confirmation that
the interim E1 allowlist is fully absorbed (no dual enforcement paths); the
lattice-to-policy mapping table (which old guarantees survived, which were
dropped deliberately).
