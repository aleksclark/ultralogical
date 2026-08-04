/**
 * Collection data layer — the ONLY module allowed to invoke List* RPCs.
 *
 * Routes and components must call listCollection / useCollection and must
 * never import AdminReadService list methods directly. CI gate:
 * scripts/check-import-gates.mjs
 */
import type { AdminClient } from "@/lib/client";
import type { CollectionPage, ListFn, QueryState } from "@/query/types";
import { pageFrom, toSearchRequest } from "./search";

export type CollectionName =
  | "tenants"
  | "api_keys"
  | "sessions"
  | "events"
  | "runs"
  | "run_steps"
  | "resources"
  | "providers"
  | "credentials"
  | "periodic_prompts"
  | "memory"
  | "waits"
  | "jobs";

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyItem = any;

function makeList(
  client: AdminClient,
  name: CollectionName,
): ListFn<AnyItem> {
  return async (state: QueryState, signal: AbortSignal): Promise<CollectionPage<AnyItem>> => {
    const search = toSearchRequest(state, {
      // tenants collection has no tenant_id field
      skipTenant: name === "tenants",
    });
    const opts = { signal };

    switch (name) {
      case "tenants":
        return pageFrom(await client.listTenants({ search }, opts));
      case "api_keys":
        return pageFrom(await client.listAPIKeys({ search }, opts));
      case "sessions":
        return pageFrom(await client.listSessions({ search }, opts));
      case "events":
        return pageFrom(await client.listEvents({ search }, opts));
      case "runs":
        return pageFrom(await client.listRuns({ search }, opts));
      case "run_steps":
        return pageFrom(await client.listRunSteps({ search }, opts));
      case "resources":
        return pageFrom(await client.listResources({ search }, opts));
      case "providers":
        return pageFrom(await client.listProviders({ search }, opts));
      case "credentials":
        return pageFrom(await client.listCredentials({ search }, opts));
      case "periodic_prompts":
        return pageFrom(await client.listPeriodicPrompts({ search }, opts));
      case "memory":
        return pageFrom(await client.listMemory({ search }, opts));
      case "waits":
        return pageFrom(await client.listWaits({ search }, opts));
      case "jobs":
        return pageFrom(await client.listJobs({ search }, opts));
      default: {
        const _exhaustive: never = name;
        throw new Error(`unknown collection: ${_exhaustive}`);
      }
    }
  };
}

/** Obtain a typed list function bound to the admin client. */
export function listCollection(client: AdminClient, name: CollectionName): ListFn<AnyItem> {
  return makeList(client, name);
}

export const COLLECTION_NAMES: CollectionName[] = [
  "tenants",
  "api_keys",
  "sessions",
  "events",
  "runs",
  "run_steps",
  "resources",
  "providers",
  "credentials",
  "periodic_prompts",
  "memory",
  "waits",
  "jobs",
];
