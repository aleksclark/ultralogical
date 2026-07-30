// Smoke test for the generated TypeScript client (acceptance A0.1 TS leg).
// Driven by the Go functional suite (e2e/ts_smoke_test.go), which boots the
// real stack and provides connection details via environment variables.
import { describe, expect, it } from "vitest";
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-node";
import { OrgService } from "./src/gen/ultra/v1/org_pb.js";
import { SessionService } from "./src/gen/ultra/v1/session_pb.js";
import { EventService } from "./src/gen/ultra/v1/event_pb.js";

const baseUrl = process.env.ULTRAD_URL;
const token = process.env.ULTRA_TOKEN;
const orgId = process.env.ULTRA_ORG_ID;

describe.skipIf(!baseUrl)("ultralogical TS client smoke", () => {
  const transport = createConnectTransport({
    baseUrl: baseUrl ?? "",
    httpVersion: "1.1",
  });
  const headers = { Authorization: `Bearer ${token}` };

  it("creates and fetches a session, appends and subscribes to events", async () => {
    const sessions = createClient(SessionService, transport);
    const events = createClient(EventService, transport);
    const orgs = createClient(OrgService, transport);

    // Org is visible to its member.
    const org = await orgs.getOrg({ orgId: orgId ?? "" }, { headers });
    expect(org.org?.id).toBe(orgId);

    // CreateSession -> GetSession roundtrip (A0.1).
    const created = await sessions.createSession(
      { orgId: orgId ?? "", title: "ts smoke" },
      { headers },
    );
    const sessionId = created.session?.id ?? "";
    expect(sessionId).not.toBe("");

    const fetched = await sessions.getSession({ sessionId }, { headers });
    expect(fetched.session?.title).toBe("ts smoke");
    expect(fetched.session?.orgId).toBe(orgId);

    // Append returns the produced seq; Subscribe replays it.
    const appended = await events.append(
      {
        sessionId,
        payload: {
          payload: { case: "userMessage", value: { text: "hello from ts" } },
        },
      },
      { headers },
    );
    expect(appended.seq).toBe(1n);

    for await (const resp of events.subscribe(
      { sessionId, fromSeq: 0n },
      { headers },
    )) {
      if (resp.event === undefined) continue; // keepalive
      expect(resp.event.seq).toBe(1n);
      expect(
        resp.event.payload?.payload.case === "userMessage" &&
          resp.event.payload.payload.value.text,
      ).toBe("hello from ts");
      break;
    }
  });
});
