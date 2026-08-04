import { MAX_PAGE_SIZE } from "@/query/types";
import { Button, Select } from "./ui";

export function CursorPager({
  limit,
  hasMore,
  hasPrev,
  onNext,
  onPrev,
  onLimitChange,
  rowCount,
  isLoading,
}: {
  limit: number;
  hasMore: boolean;
  hasPrev: boolean;
  onNext: () => void;
  onPrev: () => void;
  onLimitChange: (n: number) => void;
  rowCount: number;
  isLoading?: boolean;
}) {
  return (
    <div
      className="flex flex-wrap items-center justify-between gap-3 border-t px-2 py-2 text-xs text-muted-foreground"
      data-testid="cursor-pager"
    >
      <div className="tabular-nums">
        {rowCount} row{rowCount === 1 ? "" : "s"} on this page
        {hasMore ? " · more available" : ""}
      </div>
      <div className="flex items-center gap-2">
        <label className="flex items-center gap-1">
          <span>Page size</span>
          <Select
            value={String(limit)}
            onChange={(e) => onLimitChange(Number(e.target.value))}
            aria-label="Page size"
            disabled={isLoading}
          >
            {[25, 50, 100, 250].filter((n) => n <= MAX_PAGE_SIZE).map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </Select>
        </label>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={!hasPrev || isLoading}
          onClick={onPrev}
          data-testid="pager-prev"
        >
          Previous
        </Button>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={!hasMore || isLoading}
          onClick={onNext}
          data-testid="pager-next"
        >
          Next
        </Button>
      </div>
    </div>
  );
}
