import { defineConfig } from "@playwright/test";

const port = process.env.WEB_PORT ?? "5173";
const webURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  use: { baseURL: webURL },
  webServer: {
    command: `npm run dev -- --host 127.0.0.1 --port ${port}`,
    url: webURL,
    reuseExistingServer: !process.env.CI,
    env: { VITE_ULTRAD_URL: process.env.ULTRAD_URL ?? "http://127.0.0.1:8080" },
  },
});
