import { test, expect } from "@playwright/test";
import { loadEndpoints, login, waitForCollectionChrome, waitForTable } from "./helpers.js";

const ep = loadEndpoints();

test.beforeEach(async ({ page }) => {
  await login(page, ep.admin_token);
});

test.describe("shell and auth", () => {
  test("overview loads runtime health", async ({ page }) => {
    await expect(page.getByTestId("overview-page")).toBeVisible();
    await expect(page.getByTestId("overview-page").getByText("Tenants", { exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Problem views" })).toBeVisible();
  });

  test("rejects tenant-shaped keys on login", async ({ page }) => {
    await page.getByRole("button", { name: "Sign out" }).click();
    await page.getByTestId("login-token").fill("uck_tenant_key_must_not_work");
    await page.getByTestId("login-submit").click();
    await expect(page.getByText(/Tenant API keys cannot authenticate/i)).toBeVisible();
  });

  test("token not written to localStorage", async ({ page }) => {
    const ls = await page.evaluate(() => JSON.stringify(localStorage));
    expect(ls).not.toContain(ep.admin_token);
    if (ep.canary_api_key) expect(ls).not.toContain(ep.canary_api_key);
  });
});

test.describe("collection routes smoke", () => {
  const routes = [
    "/tenants",
    "/sessions",
    "/events",
    "/runs",
    "/resources",
    "/providers",
    "/jobs",
    "/automation",
    "/credentials",
    "/api-keys",
    "/security",
    "/internals",
  ];

  for (const route of routes) {
    test(`route ${route} renders`, async ({ page }) => {
      await page.goto(route);
      if (route === "/security") {
        await expect(page.getByTestId("security-page")).toBeVisible();
      } else if (route === "/internals") {
        await expect(page.getByTestId("internals-page")).toBeVisible();
      } else {
        await expect(page.locator("h1")).toBeVisible();
        await waitForCollectionChrome(page);
      }
    });
  }
});

test.describe("URL query state", () => {
  test("search and filters round-trip via URL", async ({ page }) => {
    await page.goto("/tenants");
    await waitForTable(page);

    await page.getByTestId("search-bar").fill("admin-e2e-tenant");
    await page.getByRole("button", { name: "Search" }).click();
    await page.waitForURL(/q=admin-e2e-tenant/);
    await waitForTable(page);

    const url = page.url();
    await page.reload();
    await expect(page).toHaveURL(url);
    await expect(page.getByTestId("search-bar")).toHaveValue("admin-e2e-tenant");
  });

  test("tenant scope banner appears from ?tenant=", async ({ page }) => {
    await page.goto("/tenants");
    await waitForTable(page);
    const firstLink = page.locator('[data-entity-kind="tenant"]').first();
    await expect(firstLink).toBeVisible();
    const tenantId = await firstLink.getAttribute("data-entity-id");
    expect(tenantId).toBeTruthy();

    await page.goto(`/sessions?tenant=${tenantId}`);
    await expect(page.getByTestId("tenant-scope-banner")).toBeVisible();
    await expect(page.getByTestId("tenant-scope-banner")).toContainText(tenantId!);
  });
});

test.describe("pagination bounds", () => {
  test("tenants page is bounded and next works", async ({ page }) => {
    await page.goto("/tenants?limit=25");
    await waitForTable(page);
    const table = page.getByTestId("admin-data-table");
    await expect(table).toBeVisible();
    const count = Number(await table.getAttribute("data-row-count"));
    expect(count).toBeGreaterThan(0);
    expect(count).toBeLessThanOrEqual(25);

    const next = page.getByTestId("pager-next");
    if (await next.isEnabled()) {
      await next.click();
      await page.waitForURL(/cursor=/);
      await waitForTable(page);
      const count2 = Number(await page.getByTestId("admin-data-table").getAttribute("data-row-count"));
      expect(count2).toBeGreaterThan(0);
      expect(count2).toBeLessThanOrEqual(25);
    }
  });
});

test.describe("golden workflows", () => {
  test("1. failed run → detail → related links", async ({ page }) => {
    await page.goto("/runs?f=state%3Aeq%3Afailed");
    await page.waitForTimeout(500);
    const empty = await page.getByText("No results").isVisible().catch(() => false);
    if (empty) {
      await page.goto("/runs");
    }
    await waitForTable(page);
    const runLink = page.locator('[data-entity-kind="run"]').first();
    await expect(runLink).toBeVisible();
    await runLink.click();
    await expect(page.getByTestId("run-detail")).toBeVisible({ timeout: 15_000 });
    await expect(
      page.getByRole("link", { name: /Related jobs|Resources created|Session/i }).first(),
    ).toBeVisible();
  });

  test("2. session event → actor → run filter", async ({ page }) => {
    await page.goto("/events");
    await waitForTable(page);
    // Click a data row (skip header). Virtual rows use role=row.
    const rows = page.getByTestId("admin-data-table").locator('[role="row"][data-row-key]');
    await expect(rows.first()).toBeVisible();
    await rows.first().click();
    await expect(page.getByTestId("detail-drawer")).toBeVisible();
    await expect(page.getByTestId("event-detail").or(page.getByTestId("json-viewer")).first()).toBeVisible({
      timeout: 15_000,
    });
    await expect(
      page.getByRole("link", { name: /filter runs by actor|Session/i }).first(),
    ).toBeVisible();
  });

  test("3. stuck/failed resource → provider link", async ({ page }) => {
    await page.goto("/resources?f=state%3Aeq%3Afailed");
    await page.waitForTimeout(500);
    const empty = await page.getByText("No results").isVisible().catch(() => false);
    if (empty) {
      await page.goto("/resources");
    }
    const hasTable = await page.getByTestId("admin-data-table").isVisible().catch(() => false);
    if (hasTable) {
      const link = page.locator('[data-entity-kind="resource"]').first();
      if (await link.isVisible().catch(() => false)) {
        await link.click();
        await expect(page.getByTestId("resource-detail")).toBeVisible({ timeout: 15_000 });
        await expect(
          page.getByRole("link", { name: /Provider|Related jobs|Session/i }).first(),
        ).toBeVisible();
      }
    } else {
      await expect(page.locator("h1")).toContainText("Resources");
    }
  });

  test("4. cross-tenant same query with tenant banner", async ({ page }) => {
    await page.goto("/tenants");
    await waitForTable(page);
    const ids = await page.locator('[data-entity-kind="tenant"]').evaluateAll((els) =>
      els.slice(0, 2).map((e) => e.getAttribute("data-entity-id")),
    );
    expect(ids.length).toBeGreaterThan(0);
    const t0 = ids[0]!;
    await page.goto(`/events?tenant=${t0}&q=hello`);
    await expect(page.getByTestId("tenant-scope-banner")).toContainText(t0);
    if (ids[1]) {
      await page.goto(`/events?tenant=${ids[1]}&q=hello`);
      await expect(page.getByTestId("tenant-scope-banner")).toContainText(ids[1]!);
    }
  });

  test("5. latency: oldest jobs / health overview", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByTestId("overview-page")).toBeVisible();
    await page
      .getByRole("link", { name: /Oldest scheduled jobs|Retryable jobs/i })
      .first()
      .click();
    await expect(page).toHaveURL(/\/jobs/);
    await expect(page.locator("h1")).toContainText("Jobs");
  });
});

test.describe("secrets non-disclosure", () => {
  test("canary key never appears in DOM or storage", async ({ page }) => {
    const canary = ep.canary_api_key;
    if (!canary) {
      test.skip();
      return;
    }
    for (const route of ["/credentials", "/api-keys", "/internals", "/"]) {
      await page.goto(route);
      await page.waitForTimeout(400);
      const body = await page.locator("body").innerText();
      expect(body).not.toContain(canary);
    }
    const storage = await page.evaluate(() => ({
      ls: JSON.stringify(localStorage),
      ss: JSON.stringify(sessionStorage),
    }));
    expect(storage.ls).not.toContain(canary);
    expect(storage.ss).not.toContain(canary);
  });
});

test.describe("network isolation", () => {
  test("SPA only talks to admin.v1 (plus static)", async ({ page }) => {
    const bad: string[] = [];
    page.on("request", (req) => {
      const url = req.url();
      if (
        url.includes("core.v1") ||
        url.includes("/ultra.v1") ||
        url.includes("TenantService") ||
        url.includes("@ultracore/client")
      ) {
        bad.push(url);
      }
    });
    await page.goto("/tenants");
    await waitForTable(page);
    await page.goto("/runs");
    await page.waitForTimeout(800);
    expect(bad, `forbidden network calls: ${bad.join(", ")}`).toEqual([]);
  });
});
