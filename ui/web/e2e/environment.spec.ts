import { expect, test } from "@playwright/test";

test("provisions a real environment and runs ExecPreview", async ({ page }) => {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");
  await page.getByLabel("New session title").fill("Web environment e2e");
  await page.getByRole("button", { name: "+" }).click();
  await page.getByRole("button", { name: "New environment" }).click();
  await expect(page.getByText(/main: 3/)).toBeVisible({ timeout: 45_000 });
  await page.getByLabel("Environment command").fill("echo web-environment");
  await page.getByRole("button", { name: "Run" }).click();
  await expect(page.getByTestId("env-output")).toContainText("web-environment", { timeout: 15_000 });
  await expect(page.locator('[data-kind="tool"]')).toContainText("exec: echo web-environment");
});
