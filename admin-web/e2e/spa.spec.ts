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
    "/memory",
    "/waits",
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

  test("filter chip and sort round-trip via URL", async ({ page }) => {
    await page.goto("/runs?f=state%3Aeq%3Afailed&s=-created_at&limit=25");
    await waitForCollectionChrome(page);
    await expect(page).toHaveURL(/f=state%3Aeq%3Afailed/);
    await expect(page).toHaveURL(/s=-created_at|s=%2Dcreated_at/);
    await expect(page).toHaveURL(/limit=25/);

    const url = page.url();
    await page.reload();
    await expect(page).toHaveURL(url);
    await waitForCollectionChrome(page);
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

  test("browser back restores prior list context", async ({ page }) => {
    await page.goto("/tenants?q=admin-e2e-tenant");
    await waitForTable(page);
    await expect(page.getByTestId("search-bar")).toHaveValue("admin-e2e-tenant");

    await page.goto("/runs");
    await waitForCollectionChrome(page);
    await page.goBack();
    await expect(page).toHaveURL(/\/tenants/);
    await expect(page).toHaveURL(/q=admin-e2e-tenant/);
    await expect(page.getByTestId("search-bar")).toHaveValue("admin-e2e-tenant");
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
  test("1. failed run → detail → steps/jobs/resources", async ({ page }) => {
    await page.goto("/runs?f=state%3Aeq%3Afailed");
    await waitForTable(page);
    await expect(page.getByTestId("admin-data-table")).toBeVisible();
    const runLink = page.locator('[data-entity-kind="run"]').first();
    await expect(runLink).toBeVisible();
    const runId = await runLink.getAttribute("data-entity-id");
    expect(runId).toBeTruthy();
    await runLink.click();
    const detail = page.getByTestId("run-detail");
    await expect(detail).toBeVisible({ timeout: 15_000 });
    await expect(page).toHaveURL(new RegExp(`/runs/${runId}`));
    await expect(detail.getByRole("link", { name: "Related jobs" })).toBeVisible();
    await expect(detail.getByRole("link", { name: "Resources created" })).toBeVisible();
    await expect(detail.getByRole("link", { name: "Related waits" })).toBeVisible();
    await expect(detail.getByRole("link", { name: "Session" })).toBeVisible();
    // Steps table is present (may be empty for thin seeds, but structure is required).
    await expect(detail.getByTestId("run-steps").or(detail.getByText("No steps.")).first()).toBeVisible();
  });

  test("2. session event → actor → run filter", async ({ page }) => {
    await page.goto("/events");
    await waitForTable(page);
    const rows = page.getByTestId("admin-data-table").locator('[role="row"][data-row-key]');
    await expect(rows.first()).toBeVisible();
    await rows.first().click();
    await expect(page.getByTestId("detail-drawer")).toBeVisible();
    await expect(page).toHaveURL(/detail=/);
    await expect(
      page.getByTestId("event-detail").or(page.getByTestId("json-viewer")).first(),
    ).toBeVisible({ timeout: 15_000 });
    await expect(
      page.getByRole("link", { name: /filter runs by actor|Session/i }).first(),
    ).toBeVisible();
    // Deep link is copyable: drawer selection is in the URL.
    const deep = page.url();
    expect(deep).toMatch(/detail=/);
    await page.reload();
    await expect(page.getByTestId("detail-drawer")).toBeVisible({ timeout: 15_000 });
  });

  test("3. stuck/failed resource → provider → jobs", async ({ page }) => {
    await page.goto("/resources?f=state%3Aeq%3Afailed");
    await waitForTable(page);
    await expect(page.getByTestId("admin-data-table")).toBeVisible();
    const link = page.locator('[data-entity-kind="resource"]').first();
    await expect(link).toBeVisible();
    await link.click();
    const resource = page.getByTestId("resource-detail");
    await expect(resource).toBeVisible({ timeout: 15_000 });
    await expect(resource.getByRole("link", { name: "Provider" })).toBeVisible();
    await expect(resource.getByRole("link", { name: "Related jobs" })).toBeVisible();
    await resource.getByRole("link", { name: "Provider" }).click();
    const provider = page.getByTestId("provider-detail");
    await expect(provider).toBeVisible({ timeout: 15_000 });
    await expect(provider.getByRole("link", { name: "Resources" })).toBeVisible();
    await expect(provider.getByRole("link", { name: "Related jobs" })).toBeVisible();
  });

  test("4. cross-tenant same query with tenant banner", async ({ page }) => {
    await page.goto("/tenants");
    await waitForTable(page);
    const ids = await page.locator('[data-entity-kind="tenant"]').evaluateAll((els) =>
      els.slice(0, 2).map((e) => e.getAttribute("data-entity-id")),
    );
    expect(ids.length).toBeGreaterThanOrEqual(2);
    const t0 = ids[0]!;
    const t1 = ids[1]!;
    await page.goto(`/events?tenant=${t0}&q=hello`);
    await expect(page.getByTestId("tenant-scope-banner")).toContainText(t0);
    await expect(page).toHaveURL(new RegExp(`tenant=${t0}`));
    await page.goto(`/events?tenant=${t1}&q=hello`);
    await expect(page.getByTestId("tenant-scope-banner")).toContainText(t1);
    await expect(page).toHaveURL(new RegExp(`tenant=${t1}`));
    // Same free-text query preserved across explicit tenant scopes.
    await expect(page.getByTestId("search-bar")).toHaveValue("hello");
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
    await waitForCollectionChrome(page);
    // Deep link carries sort or filter for the problem view.
    await expect(page).toHaveURL(/[?&](s|f)=/);
  });
});

test.describe("secrets non-disclosure", () => {
  test("canary key never appears in DOM or storage", async ({ page }) => {
    const canary = ep.canary_api_key;
    if (!canary) {
      test.skip();
      return;
    }
    for (const route of ["/credentials", "/api-keys", "/internals", "/memory", "/"]) {
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
    await page.goto("/memory");
    await page.waitForTimeout(500);
    expect(bad, `forbidden network calls: ${bad.join(", ")}`).toEqual([]);
  });
});
