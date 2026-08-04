import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import {
  defaultQueryState,
  queryStateFromSearchParams,
  queryStateToSearchParams,
  shouldResetCursor,
  withResetCursor,
} from "./state";
import type { QueryFilter, QuerySort, QueryState } from "./types";

/**
 * URL-backed query state hook. Every collection route must use this (or a
 * thin wrapper) so back/forward/refresh/deep-link reproduce the same view.
 */
export function useQueryState(defaults?: Partial<QueryState>): {
  state: QueryState;
  setState: (patch: Partial<QueryState> | ((s: QueryState) => QueryState)) => void;
  replaceState: (next: QueryState) => void;
  setSearch: (q: string) => void;
  setFilters: (filters: QueryFilter[]) => void;
  setSorts: (sorts: QuerySort[]) => void;
  setLimit: (limit: number) => void;
  setTenantId: (tenantId: string) => void;
  setColumns: (columns: string[]) => void;
  setViewId: (viewId: string) => void;
  setDetail: (detail: string) => void;
  goNext: (nextCursor: string) => void;
  goPrev: () => void;
  resetCursor: () => void;
} {
  const [params, setParams] = useSearchParams();

  const state = useMemo(() => {
    const fromUrl = queryStateFromSearchParams(params);
    const base = defaultQueryState(defaults);
    return {
      ...base,
      ...fromUrl,
      // defaults fill empty columns only
      columns: fromUrl.columns.length ? fromUrl.columns : base.columns,
      limit: fromUrl.limit || base.limit,
    };
  }, [params, defaults]);

  const write = useCallback(
    (next: QueryState, replace = false) => {
      const sp = queryStateToSearchParams(next);
      setParams(sp, { replace });
    },
    [setParams],
  );

  const setState = useCallback(
    (patch: Partial<QueryState> | ((s: QueryState) => QueryState)) => {
      const nextRaw = typeof patch === "function" ? patch(state) : { ...state, ...patch };
      let next = nextRaw;
      if (shouldResetCursor(state, nextRaw)) {
        next = withResetCursor(nextRaw);
      }
      write(next);
    },
    [state, write],
  );

  const replaceState = useCallback((next: QueryState) => write(next, true), [write]);

  return {
    state,
    setState,
    replaceState,
    setSearch: (q) => setState({ q }),
    setFilters: (filters) => setState({ filters }),
    setSorts: (sorts) => setState({ sorts }),
    setLimit: (limit) => setState({ limit }),
    setTenantId: (tenantId) => setState({ tenantId }),
    // column / view changes must NOT reset cursor — set directly
    setColumns: (columns) => write({ ...state, columns }),
    setViewId: (viewId) => write({ ...state, viewId }),
    setDetail: (detail) => write({ ...state, detail }, true),
    goNext: (nextCursor: string) => {
      if (!nextCursor) return;
      write({
        ...state,
        cursorStack: [...state.cursorStack, state.cursor],
        cursor: nextCursor,
      });
    },
    goPrev: () => {
      if (!state.cursorStack.length) {
        write({ ...state, cursor: "" });
        return;
      }
      const stack = [...state.cursorStack];
      const prev = stack.pop() ?? "";
      write({ ...state, cursorStack: stack, cursor: prev });
    },
    resetCursor: () => write(withResetCursor(state)),
  };
}
