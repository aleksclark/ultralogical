# Throughput baseline

This is a **regression baseline**, not a capacity claim. The numbers below
describe one specific workload on one specific machine with a scripted model
that does no network I/O. They say nothing about how many real agent runs the
system can serve, because a real run's cost is dominated by the provider call.

Use it exactly one way: run the same test on the same machine before and after
a change, and compare the two artifacts. A number moving by an order of
magnitude is a bug worth investigating. A number moving by 30% between two
different machines is meaningless.

## Producing an artifact

```sh
go test ./e2e/ -run TestA88_ThroughputBaseline -v -count=1 -timeout 25m
```

The test logs a JSON document prefixed with `ULTRA_THROUGHPUT_BASELINE`. To
capture it to a file for diffing:

```sh
ULTRA_BASELINE_OUT=/tmp/baseline-before.json \
  go test ./e2e/ -run TestA88_ThroughputBaseline -count=1 -timeout 25m
```

The artifact schema is `ultracore.throughput_baseline.v1`
(`e2e/phase8_throughput_test.go`).

## Workload definition

Changing any of these constants invalidates every previously recorded
artifact, so they live as named constants in the test rather than inline.

| Property | Value |
|---|---|
| Sessions | 3 |
| Runs per session | 4 (12 total, all started concurrently) |
| Steps per run | 3 — two `post_event` tool steps, then a final answer |
| cored replicas | 2, behind a round-robin ingress |
| Workers | 2 |
| Queue | river on Postgres, worker defaults |
| Model | scripted `modelscript` server, no network, no artificial delay |
| Subscribers | one per (replica, session) — 6 total, none of which started the work |

Every run goes through the full durable loop: job claim, model call, tool
dispatch, transactional event append, and re-enqueue. Runs are started
concurrently so the measurement reflects simultaneous load across both
replicas rather than a serial drip.

## What is measured

- **Run latency** — from `StartRun` returning to the run reaching
  `RUN_STATE_COMPLETED`, summarized as min/p50/p95/p99/max.
- **Event delivery lag** — from the server-assigned event timestamp to the
  moment a subscriber on a *different* replica received it. This is the number
  that matters for a distributed session.
- **Throughput** — runs and steps per second of wall clock across the whole
  concurrent batch.
- **Retries** — how many step rows recorded an attempt above 1.

## What is asserted

The test asserts invariants that must hold at any speed:

- every run started and reached `completed`;
- no run executed the same step index twice;
- every run recorded at least the scripted step count;
- the queue holds zero runnable jobs once the workload reports done (a
  baseline taken while work is still draining is not a baseline);
- subscribers on both replicas actually received events;
- no event was delivered meaningfully before its own server timestamp.

It also asserts two deliberately generous ceilings — 3 minutes p99 run latency
and 45 seconds p99 event lag — whose only job is to catch an order-of-magnitude
regression on unknown CI hardware. They are not targets, and tightening them
to "look good" would make the suite flaky without making the system faster.

## Reference artifact

Recorded 2026-08-01 on a developer workstation. Reproduce on your own hardware
before comparing.

| Property | Value |
|---|---|
| OS / arch | linux / amd64 |
| CPUs | 32 |
| Go | 1.26.5 |
| CI | no |

| Measurement | Value |
|---|---|
| Wall clock | 384 ms |
| Runs/second | 31.3 |
| Steps/second | 93.9 |
| Run latency p50 / p95 / p99 | 324 ms / 383 ms / 383 ms |
| Event delivery lag p50 / p95 / p99 | 2.6 ms / 13.3 ms / 15.7 ms |
| Steps recorded | 36 |
| Step retries | 0 |
| Events delivered to cross-replica subscribers | 432 |

Zero retries is the expected steady state: a nonzero value here on an
otherwise idle machine means the queue is redelivering work that did not fail,
which is worth investigating even though the test tolerates it.
