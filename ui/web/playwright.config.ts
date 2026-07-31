import { defineConfig } from "@playwright/test";

const port = process.env.WEB_PORT ?? "5173";
const webURL = `http://127.0.0.1:${port}`;
// The suite drives the production build, not the dev server: that is the
// artifact users get, and it removes on-demand transformation as a source of
// cold-start flake on the first navigation. Set WEB_PREVIEW=0 to iterate
// against the dev server locally.
const preview = process.env.WEB_PREVIEW !== "0";

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  // The application mounts and then loads orgs and sessions over the network,
  // so a spec's first assertion can outlast the default expect timeout on a
  // cold, contended machine.
  expect: { timeout: 20_000 },
  use: { baseURL: webURL, actionTimeout: 20_000 },
  webServer: {
    command: preview
      ? `npm run build && npx vite preview --host 127.0.0.1 --port ${port} --strictPort`
      : `npm run dev -- --host 127.0.0.1 --port ${port}`,
    url: webURL,
    timeout: 180_000,
    reuseExistingServer: !process.env.CI,
    env: { VITE_ULTRAD_URL: process.env.ULTRAD_URL ?? "http://127.0.0.1:8080" },
  },
});
