import { expect, test } from "@playwright/test";

test("registers provider kinds in dark-mode shadcn settings", async ({ page }) => {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");
  await expect(page.locator("html")).toHaveClass(/dark/);
  await page.getByRole("button", { name: "Settings" }).click();
  await expect(page.getByText("Provider instances")).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Provider kind" })).toContainText("Kubernetes");
  await expect(page.getByRole("combobox", { name: "Provider kind" })).toContainText("Nomad");
  await expect(page.getByRole("combobox", { name: "Provider kind" })).toContainText("Local tunnel");
});

test("shows provider validation errors", async ({ page }) => {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");
  await page.getByRole("button", { name: "Settings" }).click();
  await page.getByRole("combobox", { name: "Provider kind" }).selectOption("byo_k8s");
  await page.getByLabel("Provider name").fill("broken-k8s");
  await page.getByLabel("Provider config JSON").fill("not-json");
  await page.getByRole("button", { name: "Register provider" }).click();
  await expect(page.getByRole("alert")).toContainText(/invalid|JSON/i);
});
