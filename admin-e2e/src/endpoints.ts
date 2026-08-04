import { readFileSync } from "node:fs";

export type Endpoints = {
  admin_url: string;
  admin_token: string;
  cored_url?: string | null;
  database_url?: string;
  canary_api_key: string;
};

/**
 * Load the endpoint JSON written by scripts/admin-e2e-stack.sh.
 * Path comes from ADMIN_E2E_ENDPOINTS, or individual env overrides for local debug.
 */
export function loadEndpoints(): Endpoints {
  const path = process.env.ADMIN_E2E_ENDPOINTS;
  if (path) {
    const raw = readFileSync(path, "utf8");
    const parsed = JSON.parse(raw) as Endpoints;
    if (!parsed.admin_url || !parsed.admin_token) {
      throw new Error(`invalid endpoints file ${path}: missing admin_url/admin_token`);
    }
    if (!parsed.canary_api_key) {
      parsed.canary_api_key = "sk-canary-XyZZy-0451-leak-detector";
    }
    return parsed;
  }

  const admin_url = process.env.ADMIN_E2E_URL ?? process.env.CORE_ADMIN_URL;
  const admin_token = process.env.ADMIN_E2E_TOKEN ?? process.env.CORE_ADMIN_TOKEN;
  if (!admin_url || !admin_token) {
    throw new Error(
      "Set ADMIN_E2E_ENDPOINTS to the stack endpoint JSON, or ADMIN_E2E_URL + ADMIN_E2E_TOKEN",
    );
  }
  return {
    admin_url,
    admin_token,
    cored_url: process.env.ADMIN_E2E_CORED_URL ?? process.env.CORED_URL ?? null,
    canary_api_key:
      process.env.ADMIN_E2E_CANARY_KEY ?? "sk-canary-XyZZy-0451-leak-detector",
  };
}
