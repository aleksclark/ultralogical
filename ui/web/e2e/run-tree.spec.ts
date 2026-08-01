import { expect, test, type Page } from "@playwright/test";

async function openSession(page: Page, title: string) {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");
  // Wait for the initial load: creating a session needs the selected org.
  await expect(page.getByLabel("Organization").locator("option")).not.toHaveCount(0);
  await page.getByLabel("New session title").fill(title);
  await page.getByRole("button", { name: "Create session" }).click();
  await expect(page.getByTestId("run-tree")).toBeVisible();
}

// A8.7 — the shipped client renders parent/child linkage. A session where
// several agents work at once is unreadable without it.
test("renders a spawned run tree with child lanes", async ({ page }) => {
  test.setTimeout(180_000);
  await openSession(page, "Run tree");

  await page.getByLabel("Prompt").fill("cohort work");
  await page.getByRole("button", { name: "Send" }).click();

  // The parent appears, then its children nest under it at greater depth.
  const nodes = page.getByTestId("run-tree-node");
  await expect(nodes.first()).toBeVisible({ timeout: 60_000 });
  await expect
    .poll(async () => nodes.count(), { timeout: 120_000, intervals: [500] })
    .toBeGreaterThanOrEqual(4); // one parent plus three cohort members

  const roots = page.locator('[data-testid="run-tree-node"][data-depth="0"]');
  const children = page.locator('[data-testid="run-tree-node"][data-depth="1"]');
  await expect(roots).toHaveCount(1);
  await expect
    .poll(async () => children.count(), { timeout: 120_000, intervals: [500] })
    .toBe(3);

  // Children really point at the parent, and carry their cohort position.
  const parentId = await roots.first().getAttribute("data-run-id");
  expect(parentId).toBeTruthy();
  for (let i = 0; i < 3; i++) {
    await expect(children.nth(i)).toHaveAttribute("data-parent-run-id", parentId!);
  }
  await expect(page.getByTestId("cohort-marker")).toHaveCount(3);

  // The parent's wait is rendered with its member count and final state.
  const wait = page.getByTestId("run-wait").first();
  await expect(wait).toBeVisible({ timeout: 120_000 });
  await expect(wait).toHaveAttribute("data-wait-kind", "cohort");
  await expect(wait).toHaveAttribute("data-member-count", "3");
  await expect
    .poll(async () => wait.getAttribute("data-wait-state"), { timeout: 120_000, intervals: [500] })
    .toBe("resolved");
});

// A8.7 — selecting a run filters the timeline to that agent's lane.
test("filters the timeline to one run lane", async ({ page }) => {
  test.setTimeout(180_000);
  await openSession(page, "Run lanes");

  await page.getByLabel("Prompt").fill("cohort work");
  await page.getByRole("button", { name: "Send" }).click();

  const timeline = page.getByTestId("timeline");
  const children = page.locator('[data-testid="run-tree-node"][data-depth="1"]');
  await expect
    .poll(async () => children.count(), { timeout: 120_000, intervals: [500] })
    .toBeGreaterThanOrEqual(1);

  // Unfiltered, the timeline shows every agent's activity.
  const allRows = Number(await timeline.getAttribute("data-visible-rows"));
  expect(allRows).toBeGreaterThan(0);

  // Selecting one child narrows it, and the lane is a strict subset.
  const childId = await children.first().getAttribute("data-run-id");
  await children.first().click();
  await expect(timeline).toHaveAttribute("data-lane", childId!);
  await expect
    .poll(async () => Number(await timeline.getAttribute("data-visible-rows")), { timeout: 30_000 })
    .toBeLessThan(allRows);

  // Every visible row belongs to the selected run.
  const visibleRunIds = await timeline.locator("[data-run-id]").evaluateAll((els) =>
    els.map((el) => el.getAttribute("data-run-id")),
  );
  for (const id of visibleRunIds) {
    expect(id).toBe(childId);
  }

  // Clearing the filter restores the full view.
  await page.getByRole("button", { name: "Show all runs" }).click();
  await expect(timeline).toHaveAttribute("data-lane", "");
  await expect
    .poll(async () => Number(await timeline.getAttribute("data-visible-rows")), { timeout: 30_000 })
    .toBeGreaterThanOrEqual(allRows);
});

// A8.7 — a wait that times out is shown as such, so an operator can tell a
// stalled parent from a progressing one.
test("shows wait and failure transitions", async ({ page }) => {
  test.setTimeout(180_000);
  await openSession(page, "Wait transitions");

  await page.getByLabel("Prompt").fill("stalling cohort");
  await page.getByRole("button", { name: "Send" }).click();

  const wait = page.getByTestId("run-wait").first();
  await expect(wait).toBeVisible({ timeout: 60_000 });
  // It is open while the member works, then times out on its deadline.
  await expect
    .poll(async () => wait.getAttribute("data-wait-state"), { timeout: 120_000, intervals: [500] })
    .toBe("timed_out");

  // The parent still completes: a timeout releases it rather than stranding it.
  await expect(page.locator('[data-run-state="completed"]').first()).toBeVisible({ timeout: 120_000 });
});

// A8.7 — memory written by an agent tool is inspectable in the browser.
test("inspects agent-written session memory", async ({ page }) => {
  test.setTimeout(180_000);
  await openSession(page, "Memory inspector");

  await page.getByLabel("Prompt").fill("remember something");
  await page.getByRole("button", { name: "Send" }).click();

  const memory = page.getByTestId("session-memory");
  await expect(memory).toBeVisible({ timeout: 120_000 });
  await memory.click();
  await expect(memory).toContainText("browser.note");
  await expect(memory).toContainText("written by an agent");
});

// A8.7 — reconnecting through another replica rebuilds the same view, proving
// the client depends on the durable log rather than on one server's memory.
test("reconnects through a second replica", async ({ page }) => {
  test.setTimeout(180_000);
  await openSession(page, "Replica reconnect");

  await page.getByLabel("Prompt").fill("cohort work");
  await page.getByRole("button", { name: "Send" }).click();

  const nodes = page.getByTestId("run-tree-node");
  await expect
    .poll(async () => nodes.count(), { timeout: 120_000, intervals: [500] })
    .toBeGreaterThanOrEqual(4);
  await expect(page.getByTestId("timeline")).toBeVisible();
  const before = await page.getByTestId("timeline").innerText();

  // Switch replicas: the subscription is torn down and rebuilt elsewhere.
  const switcher = page.getByTestId("replica-switch");
  await expect(switcher).toBeVisible();
  const firstEndpoint = await switcher.getAttribute("data-endpoint");
  await page.getByRole("button", { name: "Reconnect through another replica" }).click();
  await expect
    .poll(async () => switcher.getAttribute("data-endpoint"), { timeout: 30_000 })
    .not.toBe(firstEndpoint);

  // Reselect the session on the new replica and require the same content.
  await page.getByRole("button", { name: "Replica reconnect" }).click();
  await expect(page.getByTestId("connection-state")).toHaveAttribute("data-connection", "live", { timeout: 60_000 });
  await expect
    .poll(async () => nodes.count(), { timeout: 120_000, intervals: [500] })
    .toBeGreaterThanOrEqual(4);
  await expect
    .poll(async () => normalize(await page.getByTestId("timeline").innerText()), { timeout: 120_000, intervals: [500] })
    .toBe(normalize(before));
});

/** normalize strips the streaming caret so a settled timeline compares equal. */
function normalize(text: string): string {
  return text.replace(/▍/g, "").trim();
}
