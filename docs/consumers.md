# Embedding ultracore

Consumers (primer, curri, …) link the Go SDK (`sdk/`) or TS SDK
(`@ultracore/client`) and bring their own UI, identity, triggers, and policy.

## Tenant setup

1. Bootstrap a tenant: `CreateTenant` returns the tenant id and a one-time
   **admin** API key.
2. Mint narrower keys with `CreateAPIKey` (`KEY_SCOPE_ADMIN` or
   `KEY_SCOPE_SESSIONS`).
3. Store keys as secrets; the raw value is never listable again.
4. Put inference credentials via `CredentialService.PutCredential`
   (`kind=inference:openai|anthropic|bedrock`).

```go
client := sdk.New(sdk.Options{
    BaseURL: "http://cored:8080",
    APIKey:  os.Getenv("CORE_TOKEN"),
    Actor:   "service/primer",
})
```

```ts
import { createClient } from "@ultracore/client";
const client = createClient({
  baseUrl: process.env.CORE_URL!,
  apiKey: process.env.CORE_TOKEN!,
  actor: "service/primer",
});
```

## Actor conventions

Every request may carry `X-Core-Actor: kind/id[/display]`. The core stores and
replays Actor on events and runs; it never branches on kind. Typical values:

| kind | id example | who |
|---|---|---|
| `service` | `primer` | consumer backend |
| `student` | `jacob` | end-user attribution |
| `agent` | `<run-id>` | loop-internal (core-set) |
| `system` | `` | core-set |

Sessions-scope keys may create sessions and drive runs; admin keys manage
credentials, providers, and other keys.

## Label conventions

Sessions carry opaque indexed labels. The core never interprets keys.

Suggested patterns:

| key | value | purpose |
|---|---|---|
| `student` | opaque id | primer roster join |
| `flow` | `pr-review` | curri flow name |
| `repo` | `org/name` | source repo |
| `env` | `prod` | deployment facet |

Selectors: equality (`op="="`) and set membership (`op="in"`). AND across
selectors. Go: `sdk.LabelEq`, `sdk.Labels{}.Eq(...).Build()`. TS: `labelEq`,
`labelIn`.

## Policy patterns

`RunPolicy` is fixed at `StartRun` and immutable:

- `allow_tools` / `deny_tools` — deny wins; `"*"` means all
- `resource_kinds` — empty denies all kinds; `["*"]` allows all
- `max_children` — `0` disables spawn
- `child_inherit` — children get the parent policy verbatim

Default when omitted: tools=`*`, kinds=`*`, max_children=16, child_inherit=false.

Denied tools emit a uniform `permission_denied` event (no existence oracle).

## Event log contract

- Per-session gapless monotonic `seq`
- `Subscribe(from_seq)` is the observation surface (streaming + multiplayer + tests)
- `Get` is the non-streaming range read
- SDK subscribe reconnects and resumes from last seq (no gaps/duplicates)
- NOTIFY is a wakeup hint only

## Run lifecycle

```go
run, _, err := client.StartRun(ctx, sessionID, prompt, nil, policy)
run, err = client.AwaitRun(ctx, run.GetId(), sdk.AwaitRunOptions{})
// if awaiting:
_, err = client.AnswerRun(ctx, run.GetId(), "user reply")
```

## Providers and resources

Register a tenant-scoped provider (`ProviderService.RegisterProvider`); the
core probes the control plane and stores capabilities. Provision resources
with `ResourceService.ProvisionResource`. Lifecycle events appear on the
session log.

## Periodic prompts

`AutomationService.PutPeriodicPrompt` schedules a prompt against a session.
Used by primer for floor proof (E5).
