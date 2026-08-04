import { Code } from "@connectrpc/connect";
import type { ReactNode } from "react";
import type { CollectionStatus } from "@/data/useCollection";
import { EmptyState, ErrorState, Skeleton } from "./ui";

export function QueryBoundary({
  status,
  error,
  errorCode,
  isStale,
  onRetry,
  emptyTitle = "No results",
  emptyDescription = "Try adjusting search or filters.",
  children,
  loadingRows = 6,
}: {
  status: CollectionStatus | "loading" | "success" | "empty" | "error" | "idle";
  error?: string | null;
  errorCode?: Code | null;
  isStale?: boolean;
  onRetry?: () => void;
  emptyTitle?: string;
  emptyDescription?: string;
  children: ReactNode;
  loadingRows?: number;
}) {
  if (status === "loading" || status === "idle") {
    return (
      <div className="space-y-2 p-2" data-testid="query-loading">
        {Array.from({ length: loadingRows }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    );
  }

  if (status === "error") {
    const title =
      errorCode === Code.Unauthenticated
        ? "Authentication required"
        : errorCode === Code.PermissionDenied
          ? "Permission denied"
          : errorCode === Code.InvalidArgument
            ? "Invalid query"
            : errorCode === Code.DeadlineExceeded
              ? "Request timed out"
              : "Request failed";
    return <ErrorState title={title} message={error} onRetry={onRetry} />;
  }

  if (status === "empty") {
    return <EmptyState title={emptyTitle} description={emptyDescription} />;
  }

  return (
    <div className={isStale ? "opacity-70 transition-opacity" : undefined} data-testid="query-ready">
      {children}
    </div>
  );
}
