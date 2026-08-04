import { defineConfig } from "@playwright/test";

/**
 * API-only suite: no browser, no SPA. Tests use APIRequestContext and the
 * generated Connect admin client against a real coreadmin process.
 */
export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI ? [["list"], ["github"]] : [["list"]],
  timeout: 60_000,
  expect: { timeout: 10_000 },
  use: {
    // No baseURL browser navigation; helpers read ADMIN_E2E_ENDPOINTS.
    trace: "off",
  },
});
