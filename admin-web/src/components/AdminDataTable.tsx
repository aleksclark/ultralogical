import { useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { cn } from "@/lib/cn";

export type ColumnDef<T> = {
  id: string;
  header: string;
  width?: number | string;
  sortable?: boolean;
  cell: (row: T) => ReactNode;
  /** plain text for copy */
  getText?: (row: T) => string;
};

export function AdminDataTable<T>({
  rows,
  columns,
  rowKey,
  visibleColumns,
  sortField,
  sortDesc,
  onSort,
  onRowActivate,
  selectedKey,
  density = "normal",
  empty,
}: {
  rows: T[];
  columns: ColumnDef<T>[];
  rowKey: (row: T) => string;
  visibleColumns?: string[];
  sortField?: string;
  sortDesc?: boolean;
  onSort?: (field: string) => void;
  onRowActivate?: (row: T) => void;
  selectedKey?: string;
  density?: "compact" | "normal";
  empty?: ReactNode;
}) {
  const parentRef = useRef<HTMLDivElement>(null);
  const [focusIdx, setFocusIdx] = useState(0);

  const cols = visibleColumns?.length
    ? columns.filter((c) => visibleColumns.includes(c.id))
    : columns;

  const rowHeight = density === "compact" ? 36 : 44;
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => rowHeight,
    overscan: 8,
  });

  function onKeyDown(e: KeyboardEvent) {
    if (!rows.length) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setFocusIdx((i) => Math.min(rows.length - 1, i + 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setFocusIdx((i) => Math.max(0, i - 1));
    } else if (e.key === "Enter" && onRowActivate) {
      e.preventDefault();
      onRowActivate(rows[focusIdx]);
    } else if ((e.metaKey || e.ctrlKey) && e.key === "c") {
      const row = rows[focusIdx];
      if (!row) return;
      const text = cols
        .map((c) => c.getText?.(row) ?? String(c.cell(row) ?? ""))
        .join("\t");
      void navigator.clipboard?.writeText(text);
    }
  }

  if (!rows.length) {
    return <div className="p-4 text-sm text-muted-foreground">{empty ?? "No rows"}</div>;
  }

  return (
    <div
      className="flex min-h-0 flex-1 flex-col outline-none"
      tabIndex={0}
      onKeyDown={onKeyDown}
      role="grid"
      aria-rowcount={rows.length}
      data-testid="admin-data-table"
      data-row-count={rows.length}
    >
      <div className="sticky top-0 z-10 flex border-b bg-card/95 backdrop-blur" role="row">
        {cols.map((c) => {
          const sorted = sortField === c.id;
          return (
            <div
              key={c.id}
              role="columnheader"
              aria-sort={sorted ? (sortDesc ? "descending" : "ascending") : "none"}
              className={cn(
                "flex items-center gap-1 px-3 py-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground",
                c.sortable && onSort && "cursor-pointer select-none hover:text-foreground",
              )}
              style={{
                width: c.width ?? undefined,
                flex: c.width ? undefined : "1 1 0",
                minWidth: typeof c.width === "number" ? c.width : 80,
              }}
              onClick={() => c.sortable && onSort?.(c.id)}
            >
              {c.header}
              {sorted ? <span aria-hidden>{sortDesc ? "↓" : "↑"}</span> : null}
            </div>
          );
        })}
      </div>
      <div ref={parentRef} className="min-h-[12rem] flex-1 overflow-auto" data-testid="table-viewport">
        <div
          style={{ height: virtualizer.getTotalSize(), position: "relative", width: "100%" }}
        >
          {virtualizer.getVirtualItems().map((vr) => {
            const row = rows[vr.index];
            const key = rowKey(row);
            const selected = selectedKey === key || focusIdx === vr.index;
            return (
              <div
                key={key}
                role="row"
                data-row-key={key}
                className={cn(
                  "absolute left-0 flex w-full border-b border-border/60",
                  selected ? "bg-accent/60" : "hover:bg-muted/40",
                  onRowActivate && "cursor-pointer",
                )}
                style={{
                  height: vr.size,
                  transform: `translateY(${vr.start}px)`,
                }}
                onClick={() => {
                  setFocusIdx(vr.index);
                  onRowActivate?.(row);
                }}
              >
                {cols.map((c) => (
                  <div
                    key={c.id}
                    role="gridcell"
                    className="flex items-center overflow-hidden px-3 text-sm"
                    style={{
                      width: c.width ?? undefined,
                      flex: c.width ? undefined : "1 1 0",
                      minWidth: typeof c.width === "number" ? c.width : 80,
                    }}
                  >
                    <div className="w-full truncate">{c.cell(row)}</div>
                  </div>
                ))}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
