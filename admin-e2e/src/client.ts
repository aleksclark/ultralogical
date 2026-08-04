import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-node";
import type { Interceptor } from "@connectrpc/connect";
// Relative import so Playwright/esbuild transforms the generated TS (node_modules
// packages are not transformed, and protoc-gen-es emits .js import specifiers).
import { AdminReadService } from "./gen/admin/v1/admin_pb.js";
import type { Endpoints } from "./endpoints.js";

function bearerInterceptor(token: string): Interceptor {
  return (next) => async (req) => {
    if (token) {
      req.header.set("Authorization", `Bearer ${token}`);
    }
    return next(req);
  };
}

/** Build an AdminReadService Connect client against a running coreadmin. */
export function createAdminClient(ep: Endpoints, tokenOverride?: string | null) {
  const token = tokenOverride === undefined ? ep.admin_token : tokenOverride ?? "";
  const interceptors = token ? [bearerInterceptor(token)] : [];
  const transport = createConnectTransport({
    baseUrl: ep.admin_url,
    httpVersion: "1.1",
    interceptors,
  });
  return createClient(AdminReadService, transport);
}

export type AdminClient = ReturnType<typeof createAdminClient>;
