# Security model

This document describes the authority and secrecy guarantees ultracore
currently enforces. Claims are backed by executable tests in `e2e/`
(`tenancy_test.go`, `policy_test.go`, `phase8_security_test.go`).

Scope: tool policy, tenancy, API keys, Actor attribution, and credential
handling. Transport hardening, rate limiting, and quota are out of scope.

## 1. Identity: tenant API keys + opaque Actor

Consumers authenticate with **tenant API keys**. Human user/org-member models
are not part of the core.

| Concept | Meaning |
|---|---|
| `Tenant` | Structural tenancy boundary (`store.Tenant(id)`) |
| `API key` | Bearer credential scoped to one tenant; scopes `admin` or `sessions` |
| `Actor` | Opaque `{"kind","id","display"}` sent per call via `X-Core-Actor` |

Keys are stored as SHA-256 hash + AES-GCM ciphertext. Raw keys are returned
once at creation, registered with the process redactor, and never appear in
events or logs. Revoked keys fail closed on subsequent calls.

- `admin` keys may manage tenants, keys, credentials, and providers.
- `sessions` keys may use the session/run/resource surface only.

**Proven by:** `e2e/tenancy_test.go` (`TestA33_KeyLifecycle`,
`TestA34_ActorAttribution`).

## 2. The run policy

Every agent run carries a `RunPolicy` fixed at creation:

| Field | Meaning |
|---|---|
| `allow_tools` | Canonical tool names; `*` means all |
| `deny_tools` | Evaluated after allow; always wins |
| `resource_kinds` | Kinds this run may provision; empty=none, `["*"]=all |
| `max_children` | Spawn cap; `0` = no spawning |
| `child_inherit` | Children get this policy verbatim |

This replaces the E1 interim flat `Grants.Tools` allowlist. There is a single
enforcement path — no dual allowlist + policy.

Default API-started runs receive `DefaultRunPolicy()` (tools=`*`, kinds=`*`,
`MaxChildren=16`, `ChildInherit=false`). Callers may pass a narrower policy to
`StartRun`.

Children: if `ChildInherit`, child policy = parent policy. Otherwise a spawn
may pass a child policy that the core validates as a **subset** (`IsSubset`:
allow ⊆ parent allow−deny, kinds ⊆ kinds, MaxChildren ≤ parent).

**Proven by:** `e2e/policy_test.go`, `policy_test.go` unit tests,
`e2e/phase8_security_test.go`.

## 3. Enforcement points

**Discovery** decides what the model is offered. Native tools are omitted when
not allowed; resource tools are only discovered on ready session resources.

**Dispatch** rechecks policy when the call arrives. Discovery filtering is a
convenience; dispatch is the boundary. Denials are uniform: the string
`permission denied`, with an explicit denial stub registered for every
canonical tool the run lacks (existence-oracle defense).

Each denial appends a `permission_denied` event naming the run and tool so
operators can audit what the agent tried.

## 4. Tenancy

All tenant data access goes through `store.Tenant(id)`. Missing and
cross-tenant are indistinguishable: both return `NotFound` with message
`not found`. This applies to sessions, runs, events, resources, credentials,
provider instances, label selectors, and event subscribe. A label selector
matching another tenant's sessions returns empty, not error.

**Proven by:** `e2e/tenancy_test.go` (`TestA31_CrossTenantInvisibility`).

## 5. Wait and run-tree authority

A run may only wait on runs it actually parented. Waiting outside your own
subtree is a uniform denial.

## 6. Credentials and token scope

**Inference credentials** are tenant-scoped, name-addressed, encrypted at rest
with AES-256-GCM. Only workers decrypt them at point of use. Cleartext never
appears in events, RPC responses, or logs. Decrypted values are registered
with the process-wide redactor (literal + URL/JSON/base64 forms).

**Resource tokens** are per-resource bearer tokens minted at provision time,
stored as hash + encrypted copy, rotated on restart (epoch++).

Credentials grant no tool authority. Narrowing a child's policy narrows what
it can do regardless of which credentials exist in the tenant.

## 7. Session labels

Sessions carry consumer-defined labels (≤16 pairs, k/v ≤128 chars), indexed
for equality and set-membership selectors. Labels are mutable via
`UpdateSessionLabels`, which emits `SessionLabelsChanged`. The core never
interprets label keys.

## 8. What is not claimed

- No claim about resistance to a malicious *worker* process.
- No claim about DoS resistance, rate limiting, or quota beyond cohort bounds.
- No claim about production key management beyond the `Keyring` seam.
- No claim about monotone grant lattices or env-scoped authority — those left
  with the product lattice; consumers supply `RunPolicy`.
