import { expect, test, type Page } from "@playwright/test";

async function openSession(page: Page, title: string) {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");
  // Wait for the initial load: creating a session needs the selected org.
  await expect(page.getByLabel("Organization").locator("option")).not.toHaveCount(0);
  await page.getByLabel("New session title").fill(title);
  await page.getByRole("button", { name: "Create session" }).click();
  await expect(page.getByTestId("environment-panel")).toBeVisible();
}

// A2.6 — provision from the shipped panel, watch the lifecycle chip progress,
// and run a real command through ExecPreview.
test("provisions a real environment and runs ExecPreview", async ({ page }) => {
  await openSession(page, "Web environment e2e");

  const chip = page.getByTestId("environment-chip").first();
  await page.getByRole("button", { name: "New environment" }).click();

  // The chip must pass through a pre-ready phase rather than appear ready
  // fully formed: the panel renders lifecycle progress, not a final snapshot.
  const phases = new Set<string>();
  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    const phase = await chip.locator("[data-phase]").getAttribute("data-phase").catch(() => null);
    if (phase) phases.add(phase);
    if (phase === "ready") break;
    await page.waitForTimeout(100);
  }
  expect([...phases]).toContain("ready");

  await page.getByLabel("Environment command").fill("echo web-environment");
  await page.getByRole("button", { name: "Execute in environment" }).click();
  await expect(page.getByTestId("env-output")).toContainText("web-environment", { timeout: 30_000 });
  await expect(page.locator('[data-kind="tool"]')).toContainText("exec: echo web-environment");
});

// A7.4 — restart is a shipped control and its effect (a new token epoch) is
// visible in the browser, after which the environment still answers.
test("restarts an environment and shows a new epoch", async ({ page }) => {
  await openSession(page, "Web environment restart");

  const chip = page.getByTestId("environment-chip").first();
  await page.getByRole("button", { name: "New environment" }).click();
  await expect(chip.locator('[data-phase="ready"]')).toBeVisible({ timeout: 60_000 });
  const before = Number(await chip.getAttribute("data-epoch"));
  expect(before).toBeGreaterThanOrEqual(1);

  await chip.getByRole("button", { name: /^Restart/ }).click();
  await expect
    .poll(async () => Number(await chip.getAttribute("data-epoch")), { timeout: 90_000, intervals: [250] })
    .toBeGreaterThan(before);
  await expect(chip.locator('[data-phase="ready"]')).toBeVisible({ timeout: 90_000 });

  await page.getByLabel("Environment command").fill("echo after-restart");
  await page.getByRole("button", { name: "Execute in environment" }).click();
  await expect(page.getByTestId("env-output")).toContainText("after-restart", { timeout: 30_000 });
});

// A7.6 — org-scoped usage is a shipped view; metering an environment produces
// a visible interval that closes when that environment terminates. Usage is
// org-scoped, so the assertion targets this environment's interval rather
// than whichever interval happens to sort first.
test("shows org usage totals", async ({ page }) => {
  test.setTimeout(180_000);
  await openSession(page, "Web usage");

  const chip = page.getByTestId("environment-chip").first();
  await page.getByRole("button", { name: "New environment" }).click();
  await expect(chip.locator('[data-phase="ready"]')).toBeVisible({ timeout: 60_000 });
  const envId = await chip.getAttribute("data-env-id");
  expect(envId).toBeTruthy();
  const interval = page.locator(`[data-testid="usage-interval"][data-env-id="${envId}"]`);

  await expect(page.getByTestId("usage-panel")).toBeVisible();
  await page.getByRole("button", { name: "Refresh usage" }).click();
  await expect(interval).toBeVisible({ timeout: 30_000 });
  await expect(interval).toHaveAttribute("data-open", "true");

  await chip.getByRole("button", { name: /^Terminate/ }).click();
  await expect
    .poll(
      async () => {
        await page.getByRole("button", { name: "Refresh usage" }).click();
        return interval.getAttribute("data-open");
      },
      { timeout: 120_000, intervals: [500] },
    )
    .toBe("false");
  await expect(page.getByTestId("usage-total")).toContainText(/Total metered seconds: \d+/);
});
