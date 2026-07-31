import { expect, test, type Page } from "@playwright/test";

async function signIn(page: Page) {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Ultralogical" })).toBeVisible();
  // The org selector is populated from ListOrgs, so waiting for it means the
  // application has finished its initial load rather than merely mounted.
  await expect(page.getByLabel("Organization").locator("option")).not.toHaveCount(0);
}

async function createSession(page: Page, title: string) {
  await page.getByLabel("New session title").fill(title);
  await page.getByRole("button", { name: "Create session" }).click();
  await expect(page.getByRole("heading", { name: title })).toBeVisible();
}

// A1.6 — Browser golden. The Go harness wrapper starts the real backend and
// seeds modelscript; this spec drives only the shipped UI and public API.
test("create session, stream, answer, and reload history", async ({ page }) => {
  await signIn(page);
  await createSession(page, "Browser golden");

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

// A7.2 — the browser must render intermediate streamed states, not only the
// final text. Two independent signals are required: the shipped reducer folds
// more than one delta frame, and the rendered assistant text is observed
// growing while the run is still streaming.
test("renders incremental streamed frames before completion", async ({ page }) => {
  await signIn(page);
  await createSession(page, "Incremental rendering");

  const timeline = page.getByTestId("timeline");
  const assistant = page.locator('[data-kind="assistant"]').first();

  await page.getByLabel("Prompt").fill("stream to me");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(assistant).toBeVisible({ timeout: 30_000 });

  const observed = new Set<string>();
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const streaming = await assistant.getAttribute("data-streaming");
    const text = ((await assistant.textContent()) ?? "").replace("▍", "").trim();
    if (text) observed.add(text);
    if (streaming !== "true") break;
    await page.waitForTimeout(50);
  }

  expect(observed.size, `distinct rendered states: ${[...observed].join(" | ")}`).toBeGreaterThanOrEqual(2);
  const frames = Number(await timeline.getAttribute("data-delta-frames"));
  expect(frames, "folded streamed delta frames").toBeGreaterThanOrEqual(2);
  await expect(page.locator('[data-status="completed"]')).toBeVisible({ timeout: 30_000 });
});

// A7.2 — replay must produce the same final timeline. Reload discards all
// client state, so an identical timeline proves it is derived from the log.
test("replays identical timeline after reload", async ({ page }) => {
  await signIn(page);
  await createSession(page, "Replay equality");

  await page.getByLabel("Prompt").fill("stream to me");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator('[data-status="completed"]')).toBeVisible({ timeout: 30_000 });
  const before = (await page.locator('[data-kind="assistant"]').allInnerTexts()).map((t) => t.replace("▍", "").trim());

  await page.reload();
  await page.getByRole("button", { name: "Replay equality" }).click();
  await expect(page.locator('[data-status="completed"]')).toBeVisible({ timeout: 30_000 });
  const after = (await page.locator('[data-kind="assistant"]').allInnerTexts()).map((t) => t.replace("▍", "").trim());

  expect(after).toEqual(before);
});

// A7.3 — credential material must never be reachable from application state.
test("never exposes credential material", async ({ page }) => {
  await signIn(page);
  await createSession(page, "Credential hygiene");

  await page.getByLabel("Prompt").fill("stream to me");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator('[data-status="completed"]')).toBeVisible({ timeout: 30_000 });

  const canary = process.env.ULTRA_CANARY_KEY ?? "sk-canary-XyZZy-0451-leak-detector";
  const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }));
  const dom = (await page.content()) + stored;
  expect(dom).not.toContain(canary);
  expect(dom).not.toContain(encodeURIComponent(canary));
});

// A7.8 — the shipped surface is built from shadcn primitives in dark mode.
test("renders shadcn primitives in dark mode", async ({ page }) => {
  await signIn(page);
  await expect(page.locator("html")).toHaveClass(/dark/);

  const create = page.getByRole("button", { name: "Create session" });
  await expect(create).toHaveClass(/inline-flex/);
  await expect(create).toHaveClass(/rounded-md/);
  await expect(page.getByTestId("connection-state")).toHaveClass(/rounded-full/);
  await expect(page.getByLabel("New session title")).toHaveClass(/bg-zinc-900/);
});
