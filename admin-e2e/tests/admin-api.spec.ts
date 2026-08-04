import { test, expect, type APIRequestContext } from "@playwright/test";
import { Code, ConnectError } from "@connectrpc/connect";
import { createAdminClient } from "../src/client.js";
import { loadEndpoints, type Endpoints } from "../src/endpoints.js";

/**
 * Phase E5 admin API e2e (API-level, no SPA).
 * Requires scripts/admin-e2e-stack.sh (or task admin:e2e) to boot coreadmin
 * and write ADMIN_E2E_ENDPOINTS.
 */

let ep: Endpoints;

test.beforeAll(() => {
  ep = loadEndpoints();
});

function admin() {
  return createAdminClient(ep);
}

function connectCode(err: unknown): Code | undefined {
  if (err instanceof ConnectError) return err.code;
  return undefined;
}

function assertNoSecretLeak(blob: unknown, canary: string) {
  // Protobuf messages may contain bigint fields; stringify safely.
  const text = JSON.stringify(blob, (_k, v) => (typeof v === "bigint" ? v.toString() : v));
  expect(text, "canary secret must not appear in admin response JSON").not.toContain(canary);
  // Full tenant API key material also must not appear (uck_… raw forms).
  expect(text).not.toMatch(/uck_[0-9a-f]{64}/i);
}

test.describe("health endpoints (APIRequestContext)", () => {
  test("GET /healthz and /readyz return ok", async ({ request }) => {
    const healthz = await request.get(`${ep.admin_url}/healthz`);
    expect(healthz.status()).toBe(200);
    expect(await healthz.text()).toBe("ok");

    const readyz = await request.get(`${ep.admin_url}/readyz`);
    expect(readyz.status()).toBe(200);
    expect(await readyz.text()).toBe("ok");
  });
});

test.describe("auth fail-closed", () => {
  test("missing bearer is unauthenticated", async () => {
    const client = createAdminClient(ep, null);
    let err: unknown;
    try {
      await client.listTenants({});
    } catch (e) {
      err = e;
    }
    expect(err).toBeTruthy();
    expect(connectCode(err)).toBe(Code.Unauthenticated);
  });

  test("wrong bearer is unauthenticated", async () => {
    const client = createAdminClient(ep, "definitely-not-the-operator-token");
    let err: unknown;
    try {
      await client.listTenants({});
    } catch (e) {
      err = e;
    }
    expect(err).toBeTruthy();
    expect(connectCode(err)).toBe(Code.Unauthenticated);
  });

  test("tenant-shaped key is not accepted as operator token", async () => {
    const client = createAdminClient(ep, "uck_tenant_key_must_not_work_here");
    let err: unknown;
    try {
      await client.getRuntimeHealth({});
    } catch (e) {
      err = e;
    }
    expect(connectCode(err)).toBe(Code.Unauthenticated);
  });
});

test.describe("DescribeCollection", () => {
  test("returns collection descriptors", async () => {
    const res = await admin().describeCollection({});
    expect(res.collections.length).toBeGreaterThanOrEqual(10);
    const names = res.collections.map((c) => c.name);
    for (const need of ["tenants", "sessions", "events", "runs", "api_keys", "credentials"]) {
      expect(names, `missing collection ${need}`).toContain(need);
    }
    for (const c of res.collections) {
      expect(c.primaryKey).toBeTruthy();
      expect(c.fields.length).toBeGreaterThan(0);
    }
  });
});

test.describe("ListTenants pagination", () => {
  test("pages with cursor and rejects limit > 250", async () => {
    const client = admin();

    const page1 = await client.listTenants({
      search: { page: { limit: 25 } },
    });
    expect(page1.items.length).toBe(25);
    expect(page1.page?.hasMore).toBe(true);
    expect(page1.page?.nextCursor).toBeTruthy();

    const seen = new Set(page1.items.map((t) => t.id));
    expect(seen.size).toBe(25);

    const page2 = await client.listTenants({
      search: {
        page: { limit: 25, cursor: page1.page?.nextCursor ?? "" },
      },
    });
    expect(page2.items.length).toBeGreaterThan(0);
    for (const item of page2.items) {
      expect(seen.has(item.id), `duplicate tenant across pages: ${item.id}`).toBe(false);
      seen.add(item.id);
    }

    let overLimitErr: unknown;
    try {
      await client.listTenants({
        search: { page: { limit: 251 } },
      });
    } catch (e) {
      overLimitErr = e;
    }
    expect(overLimitErr).toBeTruthy();
    expect(connectCode(overLimitErr)).toBe(Code.InvalidArgument);
  });
});

test.describe("list smokes", () => {
  test("ListSessions / ListEvents / ListRuns return bounded pages", async () => {
    const client = admin();

    const sessions = await client.listSessions({
      search: { page: { limit: 50 } },
    });
    expect(sessions.items.length).toBeGreaterThan(0);
    expect(sessions.items.length).toBeLessThanOrEqual(50);
    expect(sessions.page).toBeTruthy();

    const events = await client.listEvents({
      search: { page: { limit: 50 } },
    });
    expect(events.items.length).toBeGreaterThan(0);
    expect(events.items.length).toBeLessThanOrEqual(50);

    const runs = await client.listRuns({
      search: { page: { limit: 50 } },
    });
    expect(runs.items.length).toBeGreaterThan(0);
    expect(runs.items.length).toBeLessThanOrEqual(50);
  });
});

test.describe("GetRuntimeHealth", () => {
  test("reports build version and river schema", async () => {
    const res = await admin().getRuntimeHealth({});
    expect(res.health).toBeTruthy();
    expect(res.health?.buildVersion).toBeTruthy();
    expect(res.health?.riverSchemaPresent).toBe(true);
    expect(res.health?.tenantCount).toBeGreaterThan(0);
  });
});

test.describe("secret non-disclosure", () => {
  test("API keys and credentials never return canary plaintext", async () => {
    const client = admin();
    const canary = ep.canary_api_key;

    const keys = await client.listAPIKeys({
      search: { page: { limit: 50 } },
    });
    expect(keys.items.length).toBeGreaterThan(0);
    assertNoSecretLeak(keys, canary);
    for (const k of keys.items) {
      expect(k.prefix).toBeTruthy();
      // key_hash_prefix is correlation metadata only.
      expect(k.keyHashPrefix === "" || k.keyHashPrefix.length <= 16).toBe(true);
    }

    const creds = await client.listCredentials({
      search: { page: { limit: 50 } },
    });
    expect(creds.items.length).toBeGreaterThan(0);
    assertNoSecretLeak(creds, canary);
    for (const c of creds.items) {
      expect(c.encrypted).toBe(true);
      expect(c.ciphertextBytes).toBeGreaterThan(0);
    }

    // Detail paths too.
    const keyDetail = await client.getAPIKey({ id: keys.items[0]!.id });
    assertNoSecretLeak(keyDetail, canary);

    const first = creds.items[0]!;
    const credDetail = await client.getCredential({
      tenantId: first.tenantId,
      kind: first.kind,
      name: first.name,
    });
    assertNoSecretLeak(credDetail, canary);
  });
});

test.describe("cored isolation", () => {
  test("cored returns non-OK for admin Connect paths when available", async ({
    request,
  }: {
    request: APIRequestContext;
  }) => {
    test.skip(!ep.cored_url, "cored_url not provided in endpoints JSON");

    const paths = [
      "/admin.v1.AdminReadService/ListTenants",
      "/admin.v1.AdminReadService/GetRuntimeHealth",
      "/admin.v1.AdminReadService/DescribeCollection",
    ];

    for (const p of paths) {
      const resp = await request.post(`${ep.cored_url}${p}`, {
        headers: {
          "content-type": "application/json",
          authorization: `Bearer ${ep.admin_token}`,
        },
        data: {},
      });
      // cored must not serve admin RPCs. Prefer 404; never 200.
      expect(resp.status(), `cored served ${p}`).not.toBe(200);
      expect([404, 405, 415, 501]).toContain(resp.status());
    }
  });
});
