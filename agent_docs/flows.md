# Flows

Flows are org-scoped, immutable versioned JSON definitions. `PutFlow`
validates prompt templates and requires at least one entry agent, then assigns
the next version. `GetFlow(version=0)` resolves latest; explicit versions
remain readable forever.

`InvokeFlow` validates required/default parameters, persists an invocation,
emits `flow_invoked`, renders each entry prompt with Go `text/template`,
creates durable runs, and transactionally enqueues their first steps. Runs
persist rendered prompts, so later flow versions cannot alter in-flight work.

Current v1 definition shape:

```json
{"params":{"subject":{"type":"string","required":true}},"agents":{"entry":{"prompt":"Review {{.subject}}","entry":true,"model":{"provider":"openai","model_id":"gpt-4.1-mini","credential":"default"}}}}
```
