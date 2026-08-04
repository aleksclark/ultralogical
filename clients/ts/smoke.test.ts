// Expanded TS SDK smoke: session → run → stream → resource lifecycle → replay.
// Driven by e2e/ts_smoke_test.go against the real stack.
import { describe, expect, it } from "vitest";
import { createClient, eventKind, RunState } from "./src/index.js";

const baseUrl = process.env.CORED_URL;
const token = process.env.CORE_TOKEN;
const tenantId = process.env.CORE_TENANT_ID;

describe.skipIf(!baseUrl)("ultracore TS SDK smoke", () => {
  const client = createClient({
    baseUrl: baseUrl ?? "",
    apiKey: token ?? "",
    actor: "service/ts-smoke",
  });

  it("session roundtrip, append, subscribe", async () => {
    const tenant = await client.tenants.getTenant({ tenantId: tenantId ?? "" });
    expect(tenant.tenant?.id).toBe(tenantId);

    const created = await client.sessions.createSession({
      tenantId: tenantId ?? "",
      title: "ts smoke",
      labels: { suite: "ts" },
    });
    const sessionId = created.session?.id ?? "";
    expect(sessionId).not.toBe("");

    const fetched = await client.sessions.getSession({ sessionId });
    expect(fetched.session?.title).toBe("ts smoke");
    expect(fetched.session?.tenantId).toBe(tenantId);

    const seq = await client.appendUserMessage(sessionId, "hello from ts");
    expect(seq).toBe(1n);

    for await (const ev of client.subscribe(sessionId, { fromSeq: 0n, reconnect: false })) {
      expect(ev.seq).toBe(1n);
      expect(eventKind(ev)).toBe("user_message");
      break;
    }

    // Replay via Get matches subscribe.
    const events = await client.getEvents(sessionId, 0n);
    expect(events.length).toBeGreaterThanOrEqual(1);
    expect(events[0]?.seq).toBe(1n);
    expect(eventKind(events[0]!)).toBe("user_message");
  });

  it("start run against modelscript and stream deltas", async () => {
    const created = await client.sessions.createSession({
      tenantId: tenantId ?? "",
      title: "ts run smoke",
    });
    const sessionId = created.session?.id ?? "";

    const { run } = await client.startRun(sessionId, "say hello");
    expect(run.id).not.toBe("");

    const seen: string[] = [];
    const deadline = Date.now() + 60_000;
    for await (const ev of client.subscribe(sessionId, { fromSeq: 0n, reconnect: false })) {
      seen.push(eventKind(ev));
      if (seen.includes("run_completed") || seen.includes("run_failed") || seen.includes("run_awaiting")) {
        break;
      }
      if (Date.now() > deadline) break;
    }
    expect(seen.some((k) => k === "run_started" || k === "text_delta" || k === "run_completed")).toBe(true);

    const terminal = await client.awaitRun(run.id, {
      timeoutMs: 30_000,
      states: [RunState.COMPLETED, RunState.FAILED, RunState.AWAITING, RunState.CANCELLED],
    });
    expect([RunState.COMPLETED, RunState.FAILED, RunState.AWAITING, RunState.CANCELLED]).toContain(
      terminal.state,
    );

    // Dump payloads for Go parity comparison (e2e/replay_parity_test.go).
    const all = await client.getEvents(sessionId, 0n);
    const payloads = all.map((e) => ({
      seq: e.seq.toString(),
      kind: eventKind(e),
    }));
    if (process.env.TS_REPLAY_OUT) {
      const fs = await import("node:fs");
      fs.writeFileSync(process.env.TS_REPLAY_OUT, JSON.stringify(payloads));
    }
  });

  it("subscribe resume has no gaps after disconnect", async () => {
    const created = await client.sessions.createSession({
      tenantId: tenantId ?? "",
      title: "ts resume",
    });
    const sessionId = created.session?.id ?? "";

    // Seed a few events.
    for (let i = 0; i < 5; i++) {
      await client.appendUserMessage(sessionId, `msg-${i}`);
    }

    const first: bigint[] = [];
    let last = 0n;
    for await (const ev of client.subscribe(sessionId, { fromSeq: 0n, reconnect: false })) {
      first.push(ev.seq);
      last = ev.seq;
      if (first.length >= 3) break;
    }

    const rest: bigint[] = [];
    for await (const ev of client.subscribe(sessionId, { fromSeq: last, reconnect: false })) {
      rest.push(ev.seq);
      if (rest.length + first.length >= 5) break;
    }

    const all = [...first, ...rest];
    for (let i = 1; i < all.length; i++) {
      expect(all[i]).toBe(all[i - 1]! + 1n);
    }
    expect(new Set(all.map(String)).size).toBe(all.length);
  });
});
