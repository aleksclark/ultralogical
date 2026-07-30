import { expect, test } from "@playwright/test";

// A1.6 — Browser golden. The Go harness wrapper starts the real backend and
// seeds modelscript; this spec drives only the public UI and API.
test("create session, stream, answer, and reload history", async ({ page }) => {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Ultralogical" })).toBeVisible();
  await page.getByLabel("New session title").fill("Browser golden");
  await page.getByRole("button", { name: "+" }).click();
  await expect(page.getByRole("heading", { name: "Browser golden" })).toBeVisible();

  await page.getByLabel("Prompt").fill("ask me something");
  await page.getByRole("button", { name: "Send" }).click();

  await expect(page.locator('[data-kind="assistant"]')).toContainText("I need input", { timeout: 30_000 });
  await expect(page.locator('[data-kind="question"]')).toContainText("Which color?");
  await page.locator('[data-kind="question"]').getByRole("button", { name: "blue" }).click();
  await expect(page.locator('[data-status="completed"]')).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('[data-kind="assistant"]').last()).toContainText("great choice of blue");

  await page.reload();
  await page.getByRole("button", { name: "Browser golden" }).click();
  await expect(page.locator('[data-kind="assistant"]')).toContainText("great choice of blue", { timeout: 15_000 });
  await expect(page.locator('[data-status="completed"]')).toBeVisible();
});
