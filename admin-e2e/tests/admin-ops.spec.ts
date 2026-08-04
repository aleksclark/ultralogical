import { test, expect } from "@playwright/test";
import { Code, ConnectError } from "@connectrpc/connect";
import { createAdminClient, createCommandClient } from "../src/client.js";
import { loadEndpoints, type Endpoints } from "../src/endpoints.js";
import { randomUUID } from "node:crypto";

/**
 * Phase E7 admin command API e2e (Playwright + Connect).
 * Complements Go task admin:security:test with client-side role/preview/audit coverage.
 */

let ep: Endpoints;

test.beforeAll(() => {
  ep = loadEndpoints();
});

function connectCode(err: unknown): Code | undefined {
  if (err instanceof ConnectError) return err.code;
  return undefined;
}

function requireRoleTokens() {
  test.skip(
    !ep.viewer_token || !ep.operator_token || !ep.security_token,
    "multi-role tokens not present in endpoints JSON (rebuild stack)",
  );
}

test.describe("E7 WhoAmI and roles", () => {
  test("admin token reports admin role and permissions", async () => {
    const me = await createAdminClient(ep).whoAmI({});
    expect(me.operator?.role).toBeTruthy();
    expect(me.operator?.permissions?.length ?? 0).toBeGreaterThan(0);
  });

  test("viewer can read but cannot cancel runs", async () => {
    requireRoleTokens();
    const viewerRead = createAdminClient(ep, ep.viewer_token);
    const me = await viewerRead.whoAmI({});
    expect(me.operator?.role).toBe("viewer");

    const runs = await viewerRead.listRuns({ search: { page: { limit: 5 } } });
    expect(runs.items.length).toBeGreaterThan(0);
    const runId = runs.items[0]!.id;

    const viewerCmd = createCommandClient(ep, ep.viewer_token);
    let err: unknown;
    try {
      await viewerCmd.cancelRun({
        options: { dryRun: true, reason: "viewer-should-fail", previewHash: "", idempotencyKey: "" },
        runId,
      });
    } catch (e) {
      err = e;
    }
    expect(err).toBeTruthy();
    expect([Code.PermissionDenied, Code.Unauthenticated]).toContain(connectCode(err));
  });
});

test.describe("E7 command preview, stale hash, execute, audit", () => {
  test("pause periodic prompt: dry-run, stale fail, execute, audit", async () => {
    requireRoleTokens();
    const read = createAdminClient(ep, ep.operator_token);
    const cmd = createCommandClient(ep, ep.operator_token);

    const prompts = await read.listPeriodicPrompts({ search: { page: { limit: 10 } } });
    expect(prompts.items.length).toBeGreaterThan(0);
    const prompt = prompts.items.find((p) => p.enabled) ?? prompts.items[0]!;
    const promptId = prompt.id;

    const preview = await cmd.pausePeriodicPrompt({
      options: { dryRun: true, reason: "e2e-preview", previewHash: "", idempotencyKey: "" },
      periodicPromptId: promptId,
    });
    expect(preview.outcome?.dryRun).toBe(true);
    const hash = preview.outcome?.preview?.previewHash;
    expect(hash).toBeTruthy();
    expect(preview.outcome?.preview?.expectedEffects?.length ?? 0).toBeGreaterThan(0);

    let staleErr: unknown;
    try {
      await cmd.pausePeriodicPrompt({
        options: {
          dryRun: false,
          previewHash: "deadbeef-not-a-real-hash",
          idempotencyKey: `stale-${randomUUID()}`,
          reason: "e2e-stale",
        },
        periodicPromptId: promptId,
      });
    } catch (e) {
      staleErr = e;
    }
    expect(connectCode(staleErr)).toBe(Code.FailedPrecondition);

    // Re-preview after stale attempt (state unchanged).
    const preview2 = await cmd.pausePeriodicPrompt({
      options: { dryRun: true, reason: "e2e-preview-2", previewHash: "", idempotencyKey: "" },
      periodicPromptId: promptId,
    });
    const hash2 = preview2.outcome?.preview?.previewHash ?? "";
    expect(hash2).toBeTruthy();

    const idem = `pause-${randomUUID()}`;
    const exec = await cmd.pausePeriodicPrompt({
      options: {
        dryRun: false,
        previewHash: hash2,
        idempotencyKey: idem,
        reason: "e2e-pause-prompt",
      },
      periodicPromptId: promptId,
    });
    expect(["ok", "already_applied"]).toContain(exec.outcome?.result ?? "");
    expect(exec.outcome?.auditEventId).toBeTruthy();

    const replay = await cmd.pausePeriodicPrompt({
      options: {
        dryRun: false,
        previewHash: hash2,
        idempotencyKey: idem,
        reason: "e2e-pause-prompt",
      },
      periodicPromptId: promptId,
    });
    expect(replay.outcome?.idempotentReplay).toBe(true);

    const audit = await createAdminClient(ep, ep.viewer_token).listAuditEvents({
      search: {
        page: { limit: 50 },
        filters: [{ field: "command", op: "eq", value: "PausePeriodicPrompt" }],
      },
    });
    expect(audit.items.length).toBeGreaterThan(0);
    const hit = audit.items.find((e) => e.id === exec.outcome?.auditEventId);
    expect(hit, "audit event from execute must be listable").toBeTruthy();
    expect(hit?.command).toBe("PausePeriodicPrompt");
    // Audit must not contain canary secret material.
    const blob = JSON.stringify(audit, (_k, v) => (typeof v === "bigint" ? v.toString() : v));
    expect(blob).not.toContain(ep.canary_api_key);
  });
});

test.describe("E7 reveal kill switch", () => {
  test("RevealSecret is unimplemented when kill switch is off", async () => {
    requireRoleTokens();
    test.skip(ep.reveal_enabled === true, "reveal enabled in this stack");

    const keys = await createAdminClient(ep, ep.security_token).listAPIKeys({
      search: { page: { limit: 5 } },
    });
    expect(keys.items.length).toBeGreaterThan(0);
    const keyId = keys.items[0]!.id;

    const sec = createCommandClient(ep, ep.security_token, {
      "X-Admin-Reauth": ep.security_token!,
    });
    let err: unknown;
    try {
      await sec.revealSecret({
        options: { dryRun: true, reason: "incident-reveal", previewHash: "", idempotencyKey: "" },
        secretKind: "api_key",
        apiKeyId: keyId,
      });
    } catch (e) {
      err = e;
    }
    expect(err).toBeTruthy();
    // Kill switch removes the capability: Unimplemented (preferred) or FailedPrecondition.
    expect([Code.Unimplemented, Code.FailedPrecondition, Code.PermissionDenied]).toContain(
      connectCode(err),
    );
  });
});

test.describe("E7 command path isolation on cored", () => {
  test("cored does not serve AdminCommandService", async ({ request }) => {
    test.skip(!ep.cored_url, "cored_url not provided");
    const resp = await request.post(`${ep.cored_url}/admin.v1.AdminCommandService/CancelRun`, {
      headers: {
        "content-type": "application/json",
        authorization: `Bearer ${ep.admin_token}`,
      },
      data: {},
    });
    expect(resp.status()).not.toBe(200);
    expect([404, 405, 415, 501]).toContain(resp.status());
  });
});
