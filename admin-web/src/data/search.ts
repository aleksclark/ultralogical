/**
 * Convert SPA QueryState → admin.v1 SearchRequest envelope.
 * ONLY the collection data layer should build list RPC inputs.
 */
import type { QueryState } from "@/query/types";
import { applyTenantFilter } from "@/query/state";

export type WireFilter = {
  field: string;
  op: string;
  value: string;
  values: string[];
};

export type WireSort = {
  field: string;
  descending: boolean;
};

export type WireSearch = {
  query: string;
  filters: WireFilter[];
  sorts: WireSort[];
  page: { limit: number; cursor: string };
};

const OP_MAP: Record<string, string> = {
  eq: "eq",
  ne: "ne",
  lt: "lt",
  lte: "lte",
  gt: "gt",
  gte: "gte",
  in: "in",
  not_in: "not_in",
  contains: "contains",
  prefix: "prefix",
  is_null: "is_null",
  is_not_null: "is_not_null",
};

export function toSearchRequest(state: QueryState, opts?: { skipTenant?: boolean }): WireSearch {
  const filters = (opts?.skipTenant ? state.filters : applyTenantFilter(state)).map((f) => ({
    field: f.field,
    op: OP_MAP[f.op] ?? f.op,
    value: f.value ?? "",
    values: f.values ?? [],
  }));
  return {
    query: state.q,
    filters,
    sorts: state.sorts.map((s) => ({ field: s.field, descending: s.descending })),
    page: {
      limit: state.limit,
      cursor: state.cursor,
    },
  };
}

export function pageFrom<T>(res: {
  items: T[];
  page?: { nextCursor?: string; hasMore?: boolean } | null;
}): { items: T[]; nextCursor: string; hasMore: boolean } {
  return {
    items: res.items ?? [],
    nextCursor: res.page?.nextCursor ?? "",
    hasMore: Boolean(res.page?.hasMore),
  };
}
