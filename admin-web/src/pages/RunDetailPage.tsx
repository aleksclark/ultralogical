import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { listFilterHref } from "@/components/EntityLink";
import { JsonViewer } from "@/components/JsonViewer";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  ErrorState,
  PageHeader,
  Skeleton,
} from "@/components/ui";
import { useCollection } from "@/data/useCollection";
import { fetchRun, fetchRunHistory } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatBytes, formatTs } from "@/lib/format";
import { defaultQueryState } from "@/query/state";
import type { RunStepSummary } from "@admin-gen/admin/v1/admin_pb";

export function RunDetailPage() {
  const { id = "" } = useParams();
  const client = useAdminClient();
  const [run, setRun] = useState<unknown>(null);
  const [history, setHistory] = useState<unknown>(null);
  const [histLoading, setHistLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const stepsState = defaultQueryState({
    filters: [{ field: "run_id", op: "eq", value: id }],
    limit: 100,
  });
  const steps = useCollection<RunStepSummary>("run_steps", stepsState);

  useEffect(() => {
    if (!id) return;
    const ac = new AbortController();
    setLoading(true);
    fetchRun(client, id, ac.signal)
      .then((res) => {
        setRun(res.item ?? res);
        setError(null);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
    return () => ac.abort();
  }, [client, id]);

  async function loadHistory() {
    setHistLoading(true);
    try {
      const res = await fetchRunHistory(client, id);
      setHistory(res.item ?? res);
    } catch (e: unknown) {
      setHistory({ error: e instanceof Error ? e.message : String(e) });
    } finally {
      setHistLoading(false);
    }
  }

  if (loading) return <Skeleton className="h-64 w-full" />;
  if (error) return <ErrorState message={error} />;

  const r = run as {
    id?: string;
    state?: string;
    sessionId?: string;
    tenantId?: string;
    failureReason?: string;
    parentRunId?: string;
    actorKind?: string;
    actorId?: string;
    actorDisplay?: string;
    createdAt?: unknown;
    updatedAt?: unknown;
    stepCount?: number;
    historyBytes?: number;
    promptBytes?: number;
    summary?: Record<string, unknown>;
  };
  const rid = r.id ?? id;
  const sessionId = r.sessionId ?? (r.summary?.sessionId as string) ?? "";
  const tenantId = r.tenantId ?? (r.summary?.tenantId as string) ?? "";
  const state = r.state ?? (r.summary?.state as string) ?? "";

  return (
    <div data-testid="run-detail">
      <PageHeader
        title={`Run ${rid.slice(0, 8)}…`}
        description={rid}
        actions={
          <>
            <Badge variant={state === "failed" ? "destructive" : "outline"}>{state || "run"}</Badge>
          </>
        }
      />

      <div className="mb-4 flex flex-wrap gap-3 text-sm">
        {sessionId ? (
          <Link className="text-primary hover:underline" to={`/sessions/${sessionId}`}>
            Session
          </Link>
        ) : null}
        {tenantId ? (
          <Link className="text-primary hover:underline" to={`/tenants/${tenantId}`}>
            Tenant
          </Link>
        ) : null}
        {r.parentRunId ? (
          <Link className="text-primary hover:underline" to={`/runs/${r.parentRunId}`}>
            Parent run
          </Link>
        ) : null}
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/jobs", [{ field: "kind", op: "contains", value: "run" }])}
        >
          Related jobs
        </Link>
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/resources", [{ field: "created_by_run_id", value: rid }])}
        >
          Resources created
        </Link>
        {r.actorId ? (
          <Link
            className="text-primary hover:underline"
            to={listFilterHref("/events", [{ field: "actor_id", value: r.actorId }])}
          >
            Events by actor
          </Link>
        ) : null}
      </div>

      {r.failureReason ? (
        <Card className="mb-3 border-destructive/40">
          <CardContent className="py-3 text-sm text-destructive">{r.failureReason}</CardContent>
        </Card>
      ) : null}

      <div className="mb-3 grid gap-3 lg:grid-cols-3">
        <Card>
          <CardContent className="space-y-1 py-3 text-sm">
            <div>
              <span className="text-muted-foreground">Actor </span>
              {r.actorKind}/{r.actorDisplay || r.actorId || "—"}
            </div>
            <div>
              <span className="text-muted-foreground">Created </span>
              {formatTs(r.createdAt as never)}
            </div>
            <div>
              <span className="text-muted-foreground">Updated </span>
              {formatTs(r.updatedAt as never)}
            </div>
            <div>
              <span className="text-muted-foreground">Prompt </span>
              {formatBytes(r.promptBytes)} · history {formatBytes(r.historyBytes)}
            </div>
          </CardContent>
        </Card>
        <Card className="lg:col-span-2">
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle>Steps</CardTitle>
            <span className="text-xs text-muted-foreground">
              {steps.data?.items.length ?? 0} loaded
            </span>
          </CardHeader>
          <CardContent>
            {(steps.data?.items.length ?? 0) === 0 ? (
              <div className="text-sm text-muted-foreground">No steps.</div>
            ) : (
              <table className="w-full text-left text-xs" data-testid="run-steps">
                <thead className="text-muted-foreground">
                  <tr>
                    <th className="py-1">#</th>
                    <th>Attempt</th>
                    <th>Tokens in/out</th>
                    <th>Finish</th>
                    <th>Created</th>
                  </tr>
                </thead>
                <tbody>
                  {steps.data!.items.map((st) => (
                    <tr key={`${st.stepIndex}-${st.attempt}`} className="border-t border-border/50">
                      <td className="py-1 font-mono">{st.stepIndex}</td>
                      <td>{st.attempt}</td>
                      <td className="font-mono">
                        {String(st.tokensIn)}/{String(st.tokensOut)}
                      </td>
                      <td>{st.finishReason || "—"}</td>
                      <td>{formatTs(st.createdAt)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="mb-3">
        <Button
          size="sm"
          variant="secondary"
          disabled={histLoading}
          onClick={() => void loadHistory()}
          data-testid="load-run-history"
        >
          {histLoading ? "Loading history…" : "Load history blob"}
        </Button>
      </div>
      {history ? <JsonViewer value={history} title="Run history blob" defaultCollapsed /> : null}

      <div className="mt-4">
        <JsonViewer value={run} title="Run detail" />
      </div>
    </div>
  );
}
