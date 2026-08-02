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
  await page.getByLabel("Provider config JSON").fill("{");
  await expect(page.getByLabel("Provider config JSON")).toHaveValue("{");
  await page.getByRole("button", { name: "Register provider" }).click();
  await expect(page.getByRole("alert")).toContainText(/invalid|JSON/i);
});

// A10.7 — registering a provider reaches a real control plane, and the
// application shows what that control plane reported it can do. The
// unsupported capabilities are shown too, with their reasons: that is what
// explains a flow being refused against this provider.
test("registers a real cluster and shows its capabilities", async ({ page }) => {
  // The suite forbids skipped specs, and the Go runner always supplies a
  // cluster, so its absence is a failure rather than a reason to pass.
  const kubeconfig = process.env.ULTRA_TEST_KUBECONFIG_BODY;
  expect(kubeconfig, "provider evidence requires a kind cluster").toBeTruthy();
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");
  await page.getByRole("button", { name: "Settings" }).click();

  const name = `web-cluster-${Date.now()}`;
  await page.getByRole("combobox", { name: "Provider kind" }).selectOption("byo_k8s");
  await page.getByLabel("Provider name").fill(name);
  await page.getByLabel("Provider config JSON").fill(
    JSON.stringify({
      kubeconfig,
      namespace: "ultra-web-registration",
      endpoint_mode: "nodeport",
      endpoint_host: "127.0.0.1",
    }),
  );
  await page.getByRole("button", { name: "Register provider" }).click();

  const row = page.locator(`[data-provider-name="${name}"]`);
  await expect(row).toBeVisible({ timeout: 30_000 });
  await expect(row).toHaveAttribute("data-kind", "byo_k8s");
  await expect(row).toHaveAttribute("data-rate-class", "byo");

  // A capability the cluster has, and one it does not, are both rendered.
  await expect(
    page.locator(`[data-provider="${name}"][data-capability="serves_tool_endpoint"]`),
  ).toHaveAttribute("data-supported", "yes");
  const missing = page.locator(
    `[data-provider="${name}"][data-capability="restart_preserves_workspace"]`,
  );
  await expect(missing).toHaveAttribute("data-supported", "no");
  await expect(missing).toContainText("unavailable");
});

// A10.7 — a registration whose control plane cannot be reached is refused and
// the reason is shown, rather than the operator being left to guess.
test("shows why an unreachable cluster was refused", async ({ page }) => {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");
  await page.getByRole("button", { name: "Settings" }).click();

  await page.getByRole("combobox", { name: "Provider kind" }).selectOption("byo_k8s");
  await page.getByLabel("Provider name").fill("web-unreachable");
  await page.getByLabel("Provider config JSON").fill(
    JSON.stringify({
      kubeconfig:
        "apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster:\n    server: http://127.0.0.1:1\ncontexts:\n- name: c\n  context:\n    cluster: c\n    user: u\ncurrent-context: c\nusers:\n- name: u\n  user: {}\n",
    }),
  );
  await page.getByRole("button", { name: "Register provider" }).click();

  await expect(page.getByRole("alert")).toContainText(/unreachable/i);
  await expect(page.locator('[data-provider-name="web-unreachable"]')).toHaveCount(0);
});

// A10.7 — an environment names the provider actually hosting it, and a
// provider that still hosts one cannot be removed. Both matter when something
// breaks: an operator has to know where an environment runs, and must not be
// able to orphan it by deleting its provider.
test("names the hosting provider and refuses to orphan environments", async ({ page }) => {
  await page.addInitScript((token) => localStorage.setItem("ultra-token", token), process.env.ULTRA_TOKEN ?? "tok-alice");
  await page.goto("/");
  await expect(page.getByLabel("Organization").locator("option")).not.toHaveCount(0);
  await page.getByLabel("New session title").fill("Web provider ownership");
  await page.getByRole("button", { name: "Create session" }).click();
  await expect(page.getByTestId("environment-panel")).toBeVisible();

  await page.getByRole("button", { name: "New environment" }).click();
  const chip = page.getByTestId("environment-chip").first();
  await expect(chip.locator('[data-phase="ready"]')).toBeVisible({ timeout: 90_000 });

  // The environment says where it runs, by name and kind rather than by id.
  const hosting = chip.getByTestId("environment-provider");
  await expect(hosting).toHaveAttribute("data-provider-name", "default");
  await expect(hosting).toHaveAttribute("data-provider-kind", "local_docker");
  await expect(hosting).toContainText("default");

  // Removing that provider is refused while it still hosts the environment.
  await page.getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Remove default" }).click();
  await expect(page.getByRole("alert")).toContainText(/still hosts/i);
  await expect(page.locator('[data-provider-name="default"]')).toBeVisible();
});
