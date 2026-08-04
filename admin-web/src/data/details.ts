/**
 * Detail / blob fetch helpers (not list RPCs). Safe to call from routes.
 */
import type { AdminClient } from "@/lib/client";

export async function fetchRuntimeHealth(client: AdminClient, signal?: AbortSignal) {
  return client.getRuntimeHealth({}, { signal });
}

export async function fetchTenant(client: AdminClient, id: string, signal?: AbortSignal) {
  return client.getTenant({ id }, { signal });
}

export async function fetchSession(client: AdminClient, id: string, signal?: AbortSignal) {
  return client.getSession({ id }, { signal });
}

export async function fetchEvent(
  client: AdminClient,
  sessionId: string,
  seq: bigint | number,
  signal?: AbortSignal,
) {
  return client.getEvent({ sessionId, seq: BigInt(seq) }, { signal });
}

export async function fetchRun(client: AdminClient, id: string, signal?: AbortSignal) {
  return client.getRun({ id }, { signal });
}

export async function fetchRunHistory(client: AdminClient, id: string, signal?: AbortSignal) {
  return client.getRunHistory({ id }, { signal });
}

export async function fetchResource(client: AdminClient, id: string, signal?: AbortSignal) {
  return client.getResource({ id }, { signal });
}

export async function fetchProvider(client: AdminClient, id: string, signal?: AbortSignal) {
  return client.getProvider({ id }, { signal });
}

export async function fetchCredential(
  client: AdminClient,
  tenantId: string,
  kind: string,
  name: string,
  signal?: AbortSignal,
) {
  return client.getCredential({ tenantId, kind, name }, { signal });
}

export async function fetchPeriodicPrompt(client: AdminClient, id: string, signal?: AbortSignal) {
  return client.getPeriodicPrompt({ id }, { signal });
}

export async function fetchMemory(
  client: AdminClient,
  sessionId: string,
  key: string,
  signal?: AbortSignal,
) {
  return client.getMemory({ sessionId, key }, { signal });
}

export async function fetchWait(client: AdminClient, id: string, signal?: AbortSignal) {
  return client.getWait({ id }, { signal });
}

export async function fetchJob(client: AdminClient, id: bigint | number | string, signal?: AbortSignal) {
  return client.getJob({ id: BigInt(id) }, { signal });
}

export async function fetchAPIKey(client: AdminClient, id: string, signal?: AbortSignal) {
  return client.getAPIKey({ id }, { signal });
}

export async function fetchSessionTimeline(
  client: AdminClient,
  sessionId: string,
  limit = 100,
  cursor = "",
  signal?: AbortSignal,
) {
  return client.getSessionTimeline(
    { sessionId, page: { limit, cursor } },
    { signal },
  );
}

export async function fetchRelated(
  client: AdminClient,
  collection: string,
  id: string,
  relation: string,
  limit = 50,
  signal?: AbortSignal,
) {
  return client.listRelated(
    { collection, id, relation, page: { limit, cursor: "" } },
    { signal },
  );
}

export async function fetchCollections(client: AdminClient, signal?: AbortSignal) {
  return client.describeCollection({ name: "" }, { signal });
}
