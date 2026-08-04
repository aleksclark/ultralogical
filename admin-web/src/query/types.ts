/** Shared query-state types for every admin collection route. */

export type FilterOp =
  | "eq"
  | "ne"
  | "lt"
  | "lte"
  | "gt"
  | "gte"
  | "in"
  | "not_in"
  | "contains"
  | "prefix"
  | "is_null"
  | "is_not_null";

export type QueryFilter = {
  field: string;
  op: FilterOp;
  value?: string;
  values?: string[];
};

export type QuerySort = {
  field: string;
  descending: boolean;
};

export const DEFAULT_PAGE_SIZE = 50;
export const MAX_PAGE_SIZE = 250;

/**
 * URL-backed query state for a collection view.
 * Changing search/filters/sorts resets cursor history.
 * Changing columns/view does not refetch.
 */
export type QueryState = {
  q: string;
  filters: QueryFilter[];
  sorts: QuerySort[];
  cursor: string;
  /** Stack of previous cursors for "Previous" navigation (not in URL as stack). */
  cursorStack: string[];
  limit: number;
  tenantId: string;
  columns: string[];
  viewId: string;
  /** Optional detail drawer selection (entity id or composite key). */
  detail: string;
};

export type CollectionPage<T> = {
  items: T[];
  nextCursor: string;
  hasMore: boolean;
};

export type ListFn<T> = (
  state: QueryState,
  signal: AbortSignal,
) => Promise<CollectionPage<T>>;
