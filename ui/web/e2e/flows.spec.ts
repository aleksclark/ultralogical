import { expect, test, type Page } from "@playwright/test";

const singleAgentFlow = JSON.stringify({
  params: { subject: { type: "string", required: true }, depth: { type: "number", default: 2 } },
  agents: {
    reviewer: { prompt: "browser flow reviewer: {{.subject}} at {{.depth}}", entry: true, tools: ["post_event"] },
  },
});

const slowFlow = JSON.stringify({
  agents: { slow: { prompt: "browser flow slow: keep going", entry: true, tools: ["post_event"] } },
});

const invalidFlow = JSON.stringify({
  agents: { reviewer: { prompt: "{{", entry: true, tools: ["bsh"] } },
});

async function openSession(page: Page, title: string) {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");
  await expect(page.getByLabel("Organization").locator("option")).not.toHaveCount(0);
  await page.getByLabel("New session title").fill(title);
  await page.getByRole("button", { name: "Create session" }).click();
  await expect(page.getByTestId("flow-panel")).toBeVisible();
}

/** saveFlow authors a version through the shipped editor. */
async function saveFlow(page: Page, name: string, definition: string) {
  await page.getByLabel("Flow definition").fill(definition);
  await page.getByLabel("Flow name").fill(name);
  await page.getByRole("button", { name: "Save version" }).click();
}

// A9.8 — the catalog lists the org's flows and a specific version can be
// selected, with the selected version's own definition shown.
test("lists the flow catalog and selects a version", async ({ page }) => {
  await openSession(page, "Web flow catalog");

  const name = `catalog-${Date.now()}`;
  await saveFlow(page, name, singleAgentFlow);
  await expect(page.getByTestId("flow-catalog")).toContainText(name);

  // A second version, then prove the selector really switches definitions
  // rather than always showing the latest.
  const second = singleAgentFlow.replace("browser flow reviewer:", "browser flow reviewer v2:");
  await saveFlow(page, name, second);
  await expect(page.getByTestId("flow-version").locator("option")).toHaveCount(2);
  await expect(page.getByLabel("Flow definition")).toContainText("reviewer v2");

  await page.getByLabel("Flow version").selectOption("1");
  await expect(page.getByLabel("Flow definition")).not.toContainText("reviewer v2");
  await expect(page.getByLabel("Flow definition")).toContainText("browser flow reviewer:");

  // The invoke form is derived from the selected version's parameters.
  await expect(page.getByTestId("flow-param")).toHaveCount(2);
  await expect(page.locator('[data-param="subject"]')).toHaveAttribute("data-type", "string");
  await expect(page.locator('[data-param="depth"]')).toHaveValue("2");
});

// A9.1/A9.8 — an invalid definition is rejected with the server's own typed
// field paths, and correcting it succeeds without reloading.
test("rejects an invalid flow with typed field paths", async ({ page }) => {
  await openSession(page, "Web flow validation");

  const name = `invalid-${Date.now()}`;
  await page.getByLabel("Flow definition").fill(invalidFlow);
  await page.getByRole("button", { name: "Validate flow" }).click();

  const errors = page.getByTestId("flow-validation-error");
  await expect(errors).not.toHaveCount(0);
  await expect(page.locator('[data-path="agents.reviewer.prompt"]')).toHaveAttribute("data-code", "invalid_template");
  await expect(page.locator('[data-path="agents.reviewer.tools[0]"]')).toHaveAttribute("data-code", "unknown_tool");

  // Saving the same definition is refused with the same structured list, so a
  // user cannot store what validation rejected.
  await page.getByLabel("Flow name").fill(name);
  await page.getByRole("button", { name: "Save version" }).click();
  await expect(page.locator('[data-path="agents.reviewer.prompt"]')).toHaveAttribute("data-code", "invalid_template");
  await expect(page.getByTestId("flow-catalog")).not.toContainText(name);

  // Correcting the definition clears the errors and stores a version.
  await saveFlow(page, name, singleAgentFlow);
  await expect(page.getByTestId("flow-validation-error")).toHaveCount(0);
  await expect(page.getByTestId("flow-catalog")).toContainText(name);
});

// A9.2/A9.3/A9.4/A9.8 — invoking from the browser shows provenance, ordered
// progress, and the topology, and links to the runs the invocation created.
test("invokes a flow and shows provenance and progress", async ({ page }) => {
  await openSession(page, "Web flow invoke");

  const name = `invoke-${Date.now()}`;
  await saveFlow(page, name, singleAgentFlow);
  await expect(page.getByTestId("flow-catalog")).toContainText(name);

  await page.getByLabel("Parameter subject").fill("browser subject");
  await page.getByRole("button", { name: "Invoke flow" }).click();

  const invocation = page.getByTestId("flow-invocation");
  await expect(invocation).toBeVisible();

  // Progress must accumulate rather than appear complete in one frame: an
  // application that only painted the final state would never show more than
  // the terminal entry.
  const seenStates = new Set<string>();
  const deadline = Date.now() + 90_000;
  while (Date.now() < deadline) {
    const state = await invocation.getAttribute("data-state");
    if (state) seenStates.add(state);
    if (state === "completed" || state === "failed") break;
    await page.waitForTimeout(100);
  }
  expect([...seenStates]).toContain("completed");
  await expect(invocation).toHaveAttribute("data-terminal-reason", "completed");

  // Provenance names the flow, its version, and the invocation.
  await expect(page.getByTestId("flow-provenance")).toContainText(`${name} v1`);
  await expect(page.getByTestId("flow-provenance")).toContainText("invocation");

  // The topology links the declared agent to the run it produced.
  const run = page.getByTestId("flow-run").first();
  await expect(run).toHaveAttribute("data-agent", "reviewer");
  await expect(run).toHaveAttribute("data-state", "completed");
  const runId = await run.getAttribute("data-run-id");
  expect(runId).toBeTruthy();

  // Ordered progress is rendered, not merely a final status.
  const keys = await page.getByTestId("flow-progress-entry").evaluateAll((nodes) =>
    nodes.map((node) => node.getAttribute("data-key")),
  );
  expect(keys).toContain("accepted");
  expect(keys).toContain("stage_started:0");
  expect(keys[keys.length - 1]).toBe("terminal");

  // The run the flow created is reachable in the session's run tree, so the
  // invocation view links real resources rather than restating its own state.
  await expect(page.getByTestId("run-tree")).toContainText("browser flow reviewer");
});

// A9.6/A9.8 — cancelling from the browser converges the invocation, and a
// reload rebuilds the same state from the server rather than from memory.
test("cancels an invocation and recovers state after reload", async ({ page }) => {
  await openSession(page, "Web flow cancel");

  const name = `cancel-${Date.now()}`;
  await saveFlow(page, name, slowFlow);
  await expect(page.getByTestId("flow-catalog")).toContainText(name);
  await page.getByRole("button", { name: "Invoke flow" }).click();

  const invocation = page.getByTestId("flow-invocation");
  await expect(invocation).toBeVisible();
  const invocationId = await invocation.getAttribute("data-invocation-id");
  expect(invocationId).toBeTruthy();
  await expect
    .poll(async () => invocation.getAttribute("data-state"), { timeout: 60_000, intervals: [200] })
    .toBe("running");

  await page.getByRole("button", { name: "Cancel invocation" }).click();
  await expect
    .poll(async () => invocation.getAttribute("data-state"), { timeout: 90_000, intervals: [250] })
    .toBe("cancelled");
  const before = await page.getByTestId("flow-progress-entry").evaluateAll((nodes) =>
    nodes.map((node) => node.getAttribute("data-key")),
  );

  // Reloading discards every in-memory view; the same invocation must come
  // back with the same terminal state and the same ordered progress.
  await page.reload();
  await page.getByRole("button", { name: "Web flow cancel" }).click();
  await expect(page.getByTestId("flow-panel")).toBeVisible();
  await page.getByTestId("flow-invocation-chip").filter({ hasText: name }).first().click();
  const restored = page.getByTestId("flow-invocation");
  await expect(restored).toHaveAttribute("data-invocation-id", invocationId!);
  await expect(restored).toHaveAttribute("data-state", "cancelled");
  await expect(restored).toHaveAttribute("data-terminal-reason", "cancelled");
  const after = await page.getByTestId("flow-progress-entry").evaluateAll((nodes) =>
    nodes.map((node) => node.getAttribute("data-key")),
  );
  expect(after).toEqual(before);
});

// A10.11 — an invocation can be opened from its identifier alone, which is the
// path an operator follows from a CLI or an alert. It must render the same
// state the session's list renders, without listing anything first.
test("opens an invocation directly by id", async ({ page }) => {
  await openSession(page, "Web flow direct route");

  const name = `direct-${Date.now()}`;
  await saveFlow(page, name, singleAgentFlow);
  await expect(page.getByTestId("flow-catalog")).toContainText(name);
  await page.getByLabel("Parameter subject").fill("direct subject");
  await page.getByRole("button", { name: "Invoke flow" }).click();

  const listed = page.getByTestId("flow-invocation");
  await expect(listed).toBeVisible();
  const invocationId = await listed.getAttribute("data-invocation-id");
  expect(invocationId).toBeTruthy();
  await expect
    .poll(async () => listed.getAttribute("data-state"), { timeout: 90_000, intervals: [250] })
    .toBe("completed");

  // What the list route rendered, recorded before navigating away.
  const listedProgress = await page.getByTestId("flow-progress-entry").evaluateAll((nodes) =>
    nodes.map((node) => node.getAttribute("data-key")),
  );
  const listedRuns = await page.getByTestId("flow-run").evaluateAll((nodes) =>
    nodes.map((node) => `${node.getAttribute("data-agent")}:${node.getAttribute("data-state")}`),
  );

  // Open the invocation by id in a fresh page: no session is selected and the
  // list was never loaded, so anything rendered came from the identifier.
  await page.goto(`/?invocation=${invocationId}`);
  const direct = page.getByTestId("flow-invocation-route");
  await expect(direct).toBeVisible();
  await expect(page.getByTestId("flow-invocation")).toHaveAttribute("data-invocation-id", invocationId!);
  await expect(page.getByTestId("flow-invocation")).toHaveAttribute("data-state", "completed");
  await expect(page.getByTestId("flow-invocation")).toHaveAttribute("data-terminal-reason", "completed");
  await expect(page.getByTestId("flow-provenance")).toContainText(`${name} v1`);

  // The direct route shows exactly what the list route showed.
  const directProgress = await page.getByTestId("flow-progress-entry").evaluateAll((nodes) =>
    nodes.map((node) => node.getAttribute("data-key")),
  );
  const directRuns = await page.getByTestId("flow-run").evaluateAll((nodes) =>
    nodes.map((node) => `${node.getAttribute("data-agent")}:${node.getAttribute("data-state")}`),
  );
  expect(directProgress).toEqual(listedProgress);
  expect(directRuns).toEqual(listedRuns);

  // An identifier that belongs to nobody is reported, not rendered as an
  // empty invocation a reader would mistake for a real one.
  await page.goto("/?invocation=00000000-0000-0000-0000-000000000000");
  await expect(page.getByTestId("flow-invocation-route")).toHaveCount(0);
});
