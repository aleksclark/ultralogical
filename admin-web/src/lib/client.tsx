/**
 * Connect-ES admin.v1 client wiring.
 *
 * Base URL defaults to same-origin so Vite's proxy (dev/preview) and any
 * reverse-proxy deployment can forward /admin.v1.* to coreadmin. Override with
 * VITE_ADMIN_API_URL when the SPA is served from a different origin and CORS
 * is configured on coreadmin (CORE_ADMIN_CORS_ORIGIN).
 */
import { createClient, type Client, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import {
  createContext,
  useContext,
  useMemo,
  type ReactNode,
} from "react";
import {
  AdminCommandService,
  AdminReadService,
} from "@admin-gen/admin/v1/admin_pb";
import { useAuth } from "./auth";

export type AdminClient = Client<typeof AdminReadService>;
export type AdminCommandClient = Client<typeof AdminCommandService>;

type Clients = {
  read: AdminClient;
  command: AdminCommandClient;
};

const ClientContext = createContext<Clients | null>(null);

function bearerInterceptor(token: string | null): Interceptor {
  return (next) => async (req) => {
    if (token) {
      req.header.set("Authorization", `Bearer ${token}`);
    }
    return next(req);
  };
}

export function adminApiBaseUrl(): string {
  const fromEnv = (import.meta.env.VITE_ADMIN_API_URL as string | undefined)?.trim();
  if (fromEnv) return fromEnv.replace(/\/$/, "");
  return "";
}

export function AdminClientProvider({ children }: { children: ReactNode }) {
  const { token } = useAuth();

  const clients = useMemo(() => {
    const transport = createConnectTransport({
      baseUrl: adminApiBaseUrl() || window.location.origin,
      interceptors: [bearerInterceptor(token)],
    });
    return {
      read: createClient(AdminReadService, transport),
      command: createClient(AdminCommandService, transport),
    };
  }, [token]);

  return <ClientContext.Provider value={clients}>{children}</ClientContext.Provider>;
}

export function useAdminClient(): AdminClient {
  const ctx = useContext(ClientContext);
  if (!ctx) throw new Error("useAdminClient requires AdminClientProvider");
  return ctx.read;
}

export function useAdminCommandClient(): AdminCommandClient {
  const ctx = useContext(ClientContext);
  if (!ctx) throw new Error("useAdminCommandClient requires AdminClientProvider");
  return ctx.command;
}
