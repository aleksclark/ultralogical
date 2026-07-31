import { expect, test } from "@playwright/test";

test("shows presence and session memory", async ({ page }) => {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");
  await page.getByLabel("New session title").fill("Web multiplayer e2e");
  await page.getByRole("button", { name: "Create session" }).click();
  await expect(page.getByTestId("presence")).toContainText("You");
});
