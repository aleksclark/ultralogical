import { expect, type Page } from "@playwright/test";
import fs from "node:fs";

export type Endpoints = {
  admin_url: string;
  admin_token: string;
  spa_url?: string | null;
  canary_api_key?: string | null;
  cored_url?: string | null;
};

export function loadEndpoints(): Endpoints {
  const path = process.env.ADMIN_E2E_ENDPOINTS;
  if (path && fs.existsSync(path)) {
    return JSON.parse(fs.readFileSync(path, "utf8")) as Endpoints;
  }
  const token = process.env.ADMIN_E2E_TOKEN || process.env.CORE_ADMIN_TOKEN;
  if (!token) {
    throw new Error("ADMIN_E2E_ENDPOINTS or ADMIN_E2E_TOKEN required");
  }
  return {
    admin_url: process.env.ADMIN_E2E_URL || "http://127.0.0.1:8082",
    admin_token: token,
    spa_url: process.env.ADMIN_WEB_URL || "http://127.0.0.1:5173",
    canary_api_key: process.env.ADMIN_E2E_CANARY_KEY || null,
  };
}

export async function login(page: Page, token: string) {
  await page.goto("/login");
  await page.getByTestId("login-token").fill(token);
  await page.getByTestId("login-submit").click();
  await expect(page.getByTestId("overview-page")).toBeVisible({ timeout: 20_000 });
}

/** Wait until a collection table or empty/error boundary is ready. */
export async function waitForTable(page: Page) {
  // Prefer the data table; fall back to empty/error states. Use first() to
  // avoid strict-mode failures when nested testids are both present.
  const table = page.getByTestId("admin-data-table");
  const empty = page.getByText("No results");
  const err = page.getByText("Request failed");
  await expect(table.or(empty).or(err).first()).toBeVisible({ timeout: 20_000 });
}

export async function waitForCollectionChrome(page: Page) {
  await expect(page.getByTestId("cursor-pager")).toBeVisible({ timeout: 20_000 });
}
