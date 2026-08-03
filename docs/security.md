# Security model

This document describes the authority and secrecy guarantees ultracore
currently enforces. Every claim below is backed by an executable example in
`e2e/phase8_security_test.go` (`TestA89_SecurityDocumentation`). The test is
the source of truth: if a claim here is not proven there, it does not belong
here.

Scope note: this covers agent tool allowlists, tenancy, and credential
handling. Transport hardening, rate limiting, audit retention, production key
management, and consumer policy hooks are out of scope for this substrate
document (E3 adds the policy hook).

## 1. The tool allowlist

Every agent run carries a `Grants` record (root package, historically
`multiplayer.go`) with a single field:

| Field | Meaning |
|---|---|
| `tools` | canonical tool names the run may call; `*` means all |

This is a **flat allowlist**, not a privilege lattice. There is no
`env_all` / `envs` env authority, no `may_spawn` / `max_children` monotone
narrowing, and no `SubsetOf` / `RootGrants` check. Children inherit the
parent's allowlist verbatim when `tools` is omitted on spawn; an explicit
empty list means no tools. E3 adds a consumer policy hook on top of this
interim safety net.

A human-started run receives server-defined default grants
(`core.DefaultGrants()`, tools=`*`). A caller may pass `grants` to
`StartRun` to launch a deliberately restricted run. An explicit full
allowlist (`*`) is accepted.

**Proven by:** `narrowing_only_at_start_run`,
`empty_allowlist_denies_at_dispatch`.

## 2. Enforcement points

Authority is checked in two distinct places, and the second one is the one
that matters.

**Discovery** decides what the model is *offered*. Native tools are omitted
when not granted; environment tools are only discovered on ready session
environments (`loop/envtools.go`, `loop/spawn.go`).

Every capability is subject to the allowlist, including the built-in session
tools. `ask_user`, `post_event`, and the four session-memory tools are gated
exactly like environment tools: a child restricted to one narrow job cannot
interrogate the human or read what everyone else in the session stored merely
because those tools are built in. A run created with an empty tool list may do
nothing at all; there is no fallback that upgrades an ungranted run to full
authority.

**Dispatch** decides what actually happens. Every native tool, every spawn,
every wait, and every environment MCP tool rechecks the run's allowlist when
the call arrives. This matters because a tool call can reach dispatch without
ever having been offered: replayed from an older step whose grants were wider,
or simply invented by the model. Discovery filtering is a convenience;
dispatch is the boundary.

Environment tool dispatch additionally rechecks that the environment is still
`ready`. A terminated environment reads as unavailable rather than silently
succeeding against a stale endpoint.

**Proven by:** `empty_allowlist_denies_at_dispatch`,
`denied_side_effect_never_happens`.

## 3. Denial visibility

Denials are uniform and non-disclosing. Every refusal — unknown tool,
ungranted tool, wait on a run you did not parent — returns exactly the string
`permission denied`, with no resource name, no identifier, and no distinction
between "you may not" and "it does not exist".

This required an explicit countermeasure. The agent framework answers a call
to an unregistered tool by listing every tool that *does* exist, which is an
existence oracle. ultracore therefore registers an explicit denial stub for
every entry in `core.CanonicalTools()` the run lacks, so a denied tool is
indistinguishable from a granted one that refused
(`StepWorker.denialStubs`).

Denials are not silent. Each one appends a `permission_denied` event naming
the run, the tool, and the environment where applicable, so a human watching
the session sees exactly what the agent tried. The event log is the audit
channel; the model's view is the opaque one.

**Proven by:** `denials_are_uniform_and_opaque`,
`denial_emits_an_audit_event`.

## 4. Tenancy

All tenant data access goes through `store.Org(id)`. Missing and cross-tenant
are indistinguishable: both return `NotFound` with the message `not found`.
A caller cannot use error codes, error text, or timing-free response shape to
learn that a resource exists in an organization they do not belong to.

This applies uniformly to sessions, runs, events, environments, credentials,
and session memory.

**Proven by:** `cross_tenant_reads_are_indistinguishable_from_missing`.

## 5. Wait and run-tree authority

A run may only wait on runs it actually parented. Waiting on an arbitrary run
id would leak both that run's existence and, on resolution, its result. An
attempt to wait outside your own subtree is a uniform denial, not a
`not found`, and not a hang.

**Proven by:** `TestA81_WaitAuthorityIsScopedToOwnChildren` (referenced, not
duplicated).

## 6. Credentials and token scope

**Inference credentials** are org-scoped and encrypted at rest with AES-256-GCM
(`secrets/`). Only workers decrypt them, at the point of use. Cleartext never
appears in an event payload, an RPC response, a database diagnostic column, or
a log line. Decrypted values are registered with the process-wide redactor,
which scrubs the literal value plus its URL-escaped, JSON-escaped, and base64
forms, because a secret that only survives redaction in its literal form is
still leaked.

**Environment tokens** are per-environment bearer tokens minted at provision
time, stored as a hash plus an encrypted copy, and decrypted only at the point
of MCP use. Every environment carries an epoch; restarting an environment
rotates the token and increments the epoch, and the tool-client cache is keyed
by epoch, so a rotated token cannot be reused by a cached client.

**Credentials are not inherited as authority.** A child agent runs against the
same org credential store as its parent — that is how it can call a model at
all — but a credential grants no tool authority. Narrowing a child's tool
allowlist narrows what it can do regardless of which credentials exist in the
org. There is no path by which possessing a credential expands the tool
allowlist.

**Proven by:** `credentials_never_leave_the_worker`,
`narrowed_child_gains_nothing_from_org_credentials`, and — for environment
token rotation and revocation specifically — `TestA74_EnvDurabilityAndRotation`,
which restarts an environment and requires that the previous token, and a
client cached with it, both stop working.

## 7. What is not claimed

- No claim about resistance to a malicious *worker* process. A worker holds
  decryption keys by design.
- No claim about denial-of-service resistance, rate limiting, or quota
  enforcement beyond cohort size bounds.
- No claim about production key management. `CORE_MASTER_KEY` is a static
  environment key behind the `Keyring` seam; a KMS-backed implementation is
  future work.
- The dev-token authenticator (`core.DevTokenAuthenticator`) is for
  development and tests only. It maps static strings to user emails and has no
  expiry, rotation, or revocation.
- No claim about monotone grant narrowing or env-scoped authority. Those left
  with the product lattice in E1; consumers supply policy in E3.
