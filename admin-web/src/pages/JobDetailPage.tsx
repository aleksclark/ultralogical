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
import { fetchJob } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatTs } from "@/lib/format";

export function JobDetailPage() {
  const { id = "" } = useParams();
  const client = useAdminClient();
  const [detail, setDetail] = useState<unknown>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!id) return;
    const ac = new AbortController();
    setLoading(true);
    fetchJob(client, id, ac.signal)
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

  const j = detail as {
    id?: bigint | number | string;
    kind?: string;
    state?: string;
    attempt?: number;
    maxAttempts?: number;
    queue?: string;
    errorsPreview?: string;
    scheduledAt?: unknown;
    createdAt?: unknown;
    attemptedAt?: unknown;
    finalizedAt?: unknown;
    summary?: Record<string, unknown>;
  };
  const jid = String(j.id ?? id);
  const kind = j.kind ?? (j.summary?.kind as string) ?? "";

  // Best-effort correlation: many job kinds embed resource/run ids in args (shown in JSON).
  return (
    <div data-testid="job-detail">
      <PageHeader
        title={`Job #${jid}`}
        description={kind}
        actions={<Badge variant="outline">{j.state ?? "job"}</Badge>}
      />

      <div className="mb-4 flex flex-wrap gap-3 text-sm">
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/jobs", [{ field: "kind", value: kind }])}
        >
          Same kind
        </Link>
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/jobs", [{ field: "state", value: "retryable" }])}
        >
          All retryable
        </Link>
        <Link className="text-primary hover:underline" to="/resources">
          Resources
        </Link>
        <Link className="text-primary hover:underline" to="/providers">
          Providers
        </Link>
      </div>

      {j.errorsPreview ? (
        <Card className="mb-3 border-destructive/40">
          <CardContent className="py-3 text-sm text-destructive">{j.errorsPreview}</CardContent>
        </Card>
      ) : null}

      <Card className="mb-3">
        <CardContent className="grid gap-2 py-3 text-sm sm:grid-cols-2">
          <div>
            <span className="text-muted-foreground">Attempt </span>
            {j.attempt}/{j.maxAttempts}
          </div>
          <div>
            <span className="text-muted-foreground">Queue </span>
            {j.queue || "—"}
          </div>
          <div>
            <span className="text-muted-foreground">Scheduled </span>
            {formatTs(j.scheduledAt as never)}
          </div>
          <div>
            <span className="text-muted-foreground">Attempted </span>
            {formatTs(j.attemptedAt as never)}
          </div>
          <div>
            <span className="text-muted-foreground">Created </span>
            {formatTs(j.createdAt as never)}
          </div>
          <div>
            <span className="text-muted-foreground">Finalized </span>
            {formatTs(j.finalizedAt as never)}
          </div>
        </CardContent>
      </Card>

      <JsonViewer value={detail} title="Job detail" />
    </div>
  );
}
