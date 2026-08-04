import {
  DEFAULT_PAGE_SIZE,
  MAX_PAGE_SIZE,
  type FilterOp,
  type QueryFilter,
  type QuerySort,
  type QueryState,
} from "./types";

const OPS: FilterOp[] = [
  "eq",
  "ne",
  "lt",
  "lte",
  "gt",
  "gte",
  "in",
  "not_in",
  "contains",
  "prefix",
  "is_null",
  "is_not_null",
];

export function defaultQueryState(overrides?: Partial<QueryState>): QueryState {
  return {
    q: "",
    filters: [],
    sorts: [],
    cursor: "",
    cursorStack: [],
    limit: DEFAULT_PAGE_SIZE,
    tenantId: "",
    columns: [],
    viewId: "",
    detail: "",
    ...overrides,
  };
}

function clampLimit(n: number): number {
  if (!Number.isFinite(n) || n <= 0) return DEFAULT_PAGE_SIZE;
  return Math.min(MAX_PAGE_SIZE, Math.max(1, Math.floor(n)));
}

/** Serialize query state into URLSearchParams (path-relative). */
export function queryStateToSearchParams(state: QueryState): URLSearchParams {
  const p = new URLSearchParams();
  if (state.q) p.set("q", state.q);
  if (state.limit !== DEFAULT_PAGE_SIZE) p.set("limit", String(state.limit));
  if (state.cursor) p.set("cursor", state.cursor);
  if (state.tenantId) p.set("tenant", state.tenantId);
  if (state.viewId) p.set("view", state.viewId);
  if (state.detail) p.set("detail", state.detail);
  if (state.columns.length) p.set("cols", state.columns.join(","));
  for (const f of state.filters) {
    const body =
      f.op === "in" || f.op === "not_in"
        ? (f.values ?? []).join("|")
        : (f.value ?? "");
    p.append("f", `${f.field}:${f.op}:${body}`);
  }
  for (const s of state.sorts) {
    p.append("s", `${s.descending ? "-" : ""}${s.field}`);
  }
  // cursor stack as opaque joined markers for back-nav restore (optional)
  if (state.cursorStack.length) {
    p.set("cstack", state.cursorStack.join("||"));
  }
  return p;
}

export function queryStateFromSearchParams(params: URLSearchParams): QueryState {
  const filters: QueryFilter[] = [];
  for (const raw of params.getAll("f")) {
    const [field, opRaw, ...rest] = raw.split(":");
    const op = opRaw as FilterOp;
    if (!field || !OPS.includes(op)) continue;
    const body = rest.join(":");
    if (op === "in" || op === "not_in") {
      filters.push({ field, op, values: body ? body.split("|") : [] });
    } else if (op === "is_null" || op === "is_not_null") {
      filters.push({ field, op });
    } else {
      filters.push({ field, op, value: body });
    }
  }

  const sorts: QuerySort[] = [];
  for (const raw of params.getAll("s")) {
    if (!raw) continue;
    if (raw.startsWith("-")) sorts.push({ field: raw.slice(1), descending: true });
    else sorts.push({ field: raw, descending: false });
  }

  const cols = params.get("cols");
  const cstack = params.get("cstack");

  return {
    q: params.get("q") ?? "",
    filters,
    sorts,
    cursor: params.get("cursor") ?? "",
    cursorStack: cstack ? cstack.split("||").filter(Boolean) : [],
    limit: clampLimit(Number(params.get("limit") ?? DEFAULT_PAGE_SIZE)),
    tenantId: params.get("tenant") ?? "",
    columns: cols ? cols.split(",").filter(Boolean) : [],
    viewId: params.get("view") ?? "",
    detail: params.get("detail") ?? "",
  };
}

/** True when a change should reset the cursor (search/filter/sort/limit/tenant). */
export function shouldResetCursor(prev: QueryState, next: QueryState): boolean {
  if (prev.q !== next.q) return true;
  if (prev.limit !== next.limit) return true;
  if (prev.tenantId !== next.tenantId) return true;
  if (JSON.stringify(prev.filters) !== JSON.stringify(next.filters)) return true;
  if (JSON.stringify(prev.sorts) !== JSON.stringify(next.sorts)) return true;
  return false;
}

export function withResetCursor(state: QueryState): QueryState {
  return { ...state, cursor: "", cursorStack: [] };
}

export function applyTenantFilter(state: QueryState): QueryFilter[] {
  const filters = [...state.filters];
  if (state.tenantId) {
    const has = filters.some((f) => f.field === "tenant_id");
    if (!has) {
      filters.push({ field: "tenant_id", op: "eq", value: state.tenantId });
    }
  }
  return filters;
}
