import { expect, test } from "@playwright/test";

// Regression: the app boots without TDZ/shadowing errors, settings expose
// all credential fields, and invalid header JSON is rejected client-side.
test("settings supports API key, base URL, and extra headers", async ({ page }) => {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Ultralogical" })).toBeVisible();
  await page.getByRole("button", { name: "Settings" }).click();
  await expect(page.getByRole("heading", { name: "Org settings" })).toBeVisible();
  await expect(page.getByLabel("OpenAI API key")).toBeVisible();
  await expect(page.getByLabel("Base URL")).toBeVisible();
  await expect(page.getByLabel("Extra headers JSON")).toHaveValue("{}");
  expect(errors).toEqual([]);

  await page.getByLabel("OpenAI API key").fill("sk-playwright-secret-value");
  await page.getByLabel("Extra headers JSON").fill('{"x-header":42}');
  await page.getByRole("button", { name: "Save credential" }).click();
  await expect(page.getByRole("alert")).toContainText("Headers must be a JSON object of string values");
});
