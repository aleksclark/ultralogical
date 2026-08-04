import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-node";
import type { Interceptor } from "@connectrpc/connect";
// Relative import so Playwright/esbuild transforms the generated TS (node_modules
// packages are not transformed, and protoc-gen-es emits .js import specifiers).
import { AdminCommandService, AdminReadService } from "./gen/admin/v1/admin_pb.js";
import type { Endpoints } from "./endpoints.js";

function bearerInterceptor(token: string, extraHeaders?: Record<string, string>): Interceptor {
  return (next) => async (req) => {
    if (token) {
      req.header.set("Authorization", `Bearer ${token}`);
    }
    if (extraHeaders) {
      for (const [k, v] of Object.entries(extraHeaders)) {
        req.header.set(k, v);
      }
    }
    return next(req);
  };
}

function transport(ep: Endpoints, token: string, extraHeaders?: Record<string, string>) {
  const interceptors = token ? [bearerInterceptor(token, extraHeaders)] : [];
  return createConnectTransport({
    baseUrl: ep.admin_url,
    httpVersion: "1.1",
    interceptors,
  });
}

/** Build an AdminReadService Connect client against a running coreadmin. */
export function createAdminClient(ep: Endpoints, tokenOverride?: string | null) {
  const token = tokenOverride === undefined ? ep.admin_token : tokenOverride ?? "";
  return createClient(AdminReadService, transport(ep, token));
}

/** Build an AdminCommandService Connect client. */
export function createCommandClient(
  ep: Endpoints,
  tokenOverride?: string | null,
  extraHeaders?: Record<string, string>,
) {
  const token = tokenOverride === undefined ? ep.admin_token : tokenOverride ?? "";
  return createClient(AdminCommandService, transport(ep, token, extraHeaders));
}

export type AdminClient = ReturnType<typeof createAdminClient>;
export type CommandClient = ReturnType<typeof createCommandClient>;
