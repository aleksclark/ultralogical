/**
 * Shared collection page composition — every list route uses this so search,
 * filters, pager, saved views, and table behavior stay consistent.
 */
import type { ReactNode } from "react";
import type { CollectionName } from "@/data/collections";
import { useCollection } from "@/data/useCollection";
import type { SavedView } from "@/data/savedViews";
import { useQueryState } from "@/query/useQueryState";
import type { FilterOp } from "@/query/types";
import { AdminDataTable, type ColumnDef } from "./AdminDataTable";
import { CursorPager } from "./CursorPager";
import { DetailDrawer } from "./DetailDrawer";
import { FilterBuilder, type FilterFieldMeta } from "./FilterBuilder";
import { QueryBoundary } from "./QueryBoundary";
import { SavedViews } from "./SavedViews";
import { SearchBar } from "./SearchBar";
import { Card, PageHeader, TenantBanner } from "./ui";

export function CollectionPage<T>({
  title,
  description,
  collection,
  columns,
  filterFields,
  rowKey,
  searchPlaceholder,
  renderDetail,
  detailTitle,
  defaultColumns,
  onRowNavigate,
  toolbarExtra,
}: {
  title: string;
  description?: string;
  collection: CollectionName;
  columns: ColumnDef<T>[];
  filterFields: FilterFieldMeta[];
  rowKey: (row: T) => string;
  searchPlaceholder?: string;
  renderDetail?: (id: string, row: T | undefined) => ReactNode;
  detailTitle?: (id: string, row: T | undefined) => string;
  defaultColumns?: string[];
  /** If set, row click navigates instead of (or in addition to) drawer. */
  onRowNavigate?: (row: T) => void;
  toolbarExtra?: ReactNode;
}) {
  const qs = useQueryState({
    columns: defaultColumns ?? columns.map((c) => c.id),
  });
  const { state, setSearch, setFilters, setSorts, setLimit, setTenantId, setDetail, setState, goNext, goPrev } =
    qs;
  const result = useCollection<T>(collection, state);
  const rows = result.data?.items ?? [];
  const selected = state.detail
    ? rows.find((r) => rowKey(r) === state.detail)
    : undefined;

  function applyView(view: SavedView) {
    setState({
      q: view.q,
      filters: view.filters,
      sorts: view.sorts,
      limit: view.limit,
      columns: view.columns,
      tenantId: view.tenantId,
      viewId: view.id,
      cursor: "",
      cursorStack: [],
    });
  }

  function toggleSort(field: string) {
    const cur = state.sorts[0];
    if (cur?.field === field) {
      setSorts([{ field, descending: !cur.descending }]);
    } else {
      setSorts([{ field, descending: true }]);
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col" data-collection={collection}>
      <PageHeader title={title} description={description} />
      <TenantBanner tenantId={state.tenantId} onClear={() => setTenantId("")} />

      <Card className="mb-3 p-3">
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-3">
            <SearchBar
              value={state.q}
              onChange={setSearch}
              placeholder={searchPlaceholder ?? `Search ${collection}…`}
            />
            <SavedViews collection={collection} state={state} onApply={applyView} />
            {toolbarExtra}
          </div>
          <FilterBuilder
            fields={
              filterFields.length
                ? filterFields
                : columns.map((c) => ({
                    name: c.id,
                    label: c.header,
                    ops: ["eq", "contains", "prefix"] as FilterOp[],
                  }))
            }
            filters={state.filters}
            onChange={setFilters}
          />
        </div>
      </Card>

      <Card className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <QueryBoundary
          status={result.status}
          error={result.error}
          errorCode={result.errorCode}
          isStale={result.isStale}
          onRetry={result.reload}
        >
          <AdminDataTable
            rows={rows}
            columns={columns}
            rowKey={rowKey}
            visibleColumns={state.columns}
            sortField={state.sorts[0]?.field}
            sortDesc={state.sorts[0]?.descending}
            onSort={toggleSort}
            selectedKey={state.detail}
            onRowActivate={(row) => {
              if (onRowNavigate) onRowNavigate(row);
              else setDetail(rowKey(row));
            }}
          />
        </QueryBoundary>
        <CursorPager
          limit={state.limit}
          hasMore={Boolean(result.data?.hasMore)}
          hasPrev={Boolean(state.cursor) || state.cursorStack.length > 0}
          onNext={() => result.data?.nextCursor && goNext(result.data.nextCursor)}
          onPrev={goPrev}
          onLimitChange={setLimit}
          rowCount={rows.length}
          isLoading={result.status === "loading"}
        />
      </Card>

      {renderDetail ? (
        <DetailDrawer
          open={Boolean(state.detail)}
          title={
            state.detail
              ? (detailTitle?.(state.detail, selected) ?? `Detail ${state.detail}`)
              : "Detail"
          }
          onClose={() => setDetail("")}
          widthClass="max-w-2xl"
        >
          {state.detail ? renderDetail(state.detail, selected) : null}
        </DetailDrawer>
      ) : null}
    </div>
  );
}
