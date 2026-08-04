/**
 * React hook that loads one page of a collection through the data layer.
 * Handles abort/stale suppression, loading/error states.
 */
import { useEffect, useRef, useState } from "react";
import { ConnectError, Code } from "@connectrpc/connect";
import { useAdminClient } from "@/lib/client";
import type { CollectionPage, QueryState } from "@/query/types";
import { listCollection, type CollectionName } from "./collections";

export type CollectionStatus = "idle" | "loading" | "success" | "empty" | "error";

export type CollectionResult<T> = {
  status: CollectionStatus;
  data: CollectionPage<T> | null;
  error: string | null;
  errorCode: Code | null;
  reload: () => void;
  isStale: boolean;
};

function errMessage(err: unknown): { message: string; code: Code | null } {
  if (err instanceof ConnectError) {
    return { message: err.message, code: err.code };
  }
  if (err instanceof DOMException && err.name === "AbortError") {
    return { message: "aborted", code: null };
  }
  if (err instanceof Error) return { message: err.message, code: null };
  return { message: String(err), code: null };
}

export function useCollection<T = unknown>(
  name: CollectionName,
  state: QueryState,
): CollectionResult<T> {
  const client = useAdminClient();
  const [status, setStatus] = useState<CollectionStatus>("loading");
  const [data, setData] = useState<CollectionPage<T> | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [errorCode, setErrorCode] = useState<Code | null>(null);
  const [tick, setTick] = useState(0);
  const [isStale, setIsStale] = useState(false);
  const reqId = useRef(0);

  // Fingerprint of fetch-relevant state (not columns/view/detail).
  const fetchKey = JSON.stringify({
    name,
    q: state.q,
    filters: state.filters,
    sorts: state.sorts,
    cursor: state.cursor,
    limit: state.limit,
    tenantId: state.tenantId,
    tick,
  });

  useEffect(() => {
    const id = ++reqId.current;
    const ac = new AbortController();
    setStatus((s) => (s === "success" || s === "empty" ? s : "loading"));
    setIsStale(true);
    setError(null);
    setErrorCode(null);

    const list = listCollection(client, name);
    list(state, ac.signal)
      .then((page) => {
        if (id !== reqId.current) return; // stale
        setData(page as CollectionPage<T>);
        setIsStale(false);
        setStatus(page.items.length === 0 ? "empty" : "success");
      })
      .catch((err: unknown) => {
        if (id !== reqId.current) return;
        if (ac.signal.aborted) return;
        const { message, code } = errMessage(err);
        if (message === "aborted") return;
        setError(message);
        setErrorCode(code);
        setIsStale(false);
        setStatus("error");
      });

    return () => {
      ac.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- fetchKey captures state
  }, [client, fetchKey]);

  return {
    status,
    data,
    error,
    errorCode,
    isStale,
    reload: () => setTick((t) => t + 1),
  };
}
