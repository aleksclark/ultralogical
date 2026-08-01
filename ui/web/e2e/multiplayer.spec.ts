import { expect, test } from "@playwright/test";

// Presence and session memory are both shipped controls: joining a session
// renders the participant, and writing a memory key through the panel renders
// an entry that survives a reload, proving it came from the durable log rather
// than local component state.
test("shows presence and session memory", async ({ page }) => {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");
  await page.getByLabel("New session title").fill("Web multiplayer e2e");
  await page.getByRole("button", { name: "Create session" }).click();
  await expect(page.getByTestId("presence")).toContainText("You");

  const memory = page.getByTestId("session-memory");
  await expect(memory).toBeVisible();
  await expect(page.getByTestId("memory-count")).toContainText("0 entries");

  await page.getByLabel("Memory key").fill("web.note");
  await page.getByLabel("Memory value").fill('"remembered"');
  await page.getByRole("button", { name: "Remember" }).click();

  const entry = page.locator('[data-testid="memory-entry"][data-key="web.note"]');
  await expect(entry).toContainText("web.note", { timeout: 15_000 });
  await expect(entry).toContainText("remembered");
  await expect(page.getByTestId("memory-count")).toContainText("1 entries");

  // A reload discards client state, so the entry must be reread from the API.
  await page.reload();
  await page.getByRole("button", { name: "Web multiplayer e2e" }).click();
  await expect(page.locator('[data-testid="memory-entry"][data-key="web.note"]')).toContainText("remembered", {
    timeout: 15_000,
  });
});
