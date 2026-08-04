import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { listFilterHref } from "@/components/EntityLink";
import { JsonViewer } from "@/components/JsonViewer";
import {
  Badge,
  Card,
  CardContent,
  ErrorState,
  PageHeader,
  Skeleton,
} from "@/components/ui";
import { fetchResource } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatTs } from "@/lib/format";

export function ResourceDetailPage() {
  const { id = "" } = useParams();
  const client = useAdminClient();
  const [detail, setDetail] = useState<unknown>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!id) return;
    const ac = new AbortController();
    setLoading(true);
    fetchResource(client, id, ac.signal)
      .then((res) => {
        setDetail(res.item ?? res);
        setError(null);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
    return () => ac.abort();
  }, [client, id]);

  if (loading) return <Skeleton className="h-64 w-full" />;
  if (error) return <ErrorState message={error} />;

  const r = detail as {
    id?: string;
    kind?: string;
    state?: string;
    tenantId?: string;
    sessionId?: string;
    providerInstanceId?: string;
    failureMessage?: string;
    createdByRunId?: string;
    endpoint?: string;
    epoch?: number;
    createdAt?: unknown;
    updatedAt?: unknown;
    summary?: Record<string, unknown>;
  };
  const rid = r.id ?? id;
  const providerId = r.providerInstanceId ?? (r.summary?.providerInstanceId as string) ?? "";
  const state = r.state ?? (r.summary?.state as string) ?? "";

  return (
    <div data-testid="resource-detail">
      <PageHeader
        title={`Resource ${rid.slice(0, 8)}…`}
        description={`${r.kind ?? ""} · ${rid}`}
        actions={<Badge variant={state === "failed" ? "destructive" : "outline"}>{state}</Badge>}
      />

      <div className="mb-4 flex flex-wrap gap-3 text-sm">
        {providerId ? (
          <Link className="text-primary hover:underline" to={`/providers/${providerId}`}>
            Provider
          </Link>
        ) : null}
        {r.sessionId ? (
          <Link className="text-primary hover:underline" to={`/sessions/${r.sessionId}`}>
            Session
          </Link>
        ) : null}
        {r.createdByRunId ? (
          <Link className="text-primary hover:underline" to={`/runs/${r.createdByRunId}`}>
            Created by run
          </Link>
        ) : null}
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/jobs", [{ field: "kind", op: "contains", value: "resource" }])}
        >
          Related jobs
        </Link>
      </div>

      {r.failureMessage ? (
        <Card className="mb-3 border-destructive/40">
          <CardContent className="py-3 text-sm text-destructive">{r.failureMessage}</CardContent>
        </Card>
      ) : null}

      <Card className="mb-3">
        <CardContent className="grid gap-2 py-3 text-sm sm:grid-cols-2">
          <div>
            <span className="text-muted-foreground">Endpoint </span>
            <span className="font-mono text-xs">{r.endpoint || "—"}</span>
          </div>
          <div>
            <span className="text-muted-foreground">Epoch </span>
            {r.epoch ?? "—"}
          </div>
          <div>
            <span className="text-muted-foreground">Created </span>
            {formatTs(r.createdAt as never)}
          </div>
          <div>
            <span className="text-muted-foreground">Updated </span>
            {formatTs(r.updatedAt as never)}
          </div>
        </CardContent>
      </Card>

      <JsonViewer value={detail} title="Resource detail (spec/handle metadata)" />
    </div>
  );
}
