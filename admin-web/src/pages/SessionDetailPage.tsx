import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { listFilterHref } from "@/components/EntityLink";
import { JsonViewer } from "@/components/JsonViewer";
import {
  Badge,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  ErrorState,
  PageHeader,
  Skeleton,
} from "@/components/ui";
import { fetchSession, fetchSessionTimeline } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatTs } from "@/lib/format";

export function SessionDetailPage() {
  const { id = "" } = useParams();
  const client = useAdminClient();
  const [session, setSession] = useState<unknown>(null);
  const [timeline, setTimeline] = useState<unknown[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!id) return;
    const ac = new AbortController();
    setLoading(true);
    Promise.all([
      fetchSession(client, id, ac.signal),
      fetchSessionTimeline(client, id, 100, "", ac.signal).catch(() => ({ items: [] })),
    ])
      .then(([s, tl]) => {
        setSession(s.item ?? s);
        setTimeline((tl as { items?: unknown[] }).items ?? []);
        setError(null);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
    return () => ac.abort();
  }, [client, id]);

  if (loading) return <Skeleton className="h-64 w-full" />;
  if (error) return <ErrorState message={error} />;

  const s = session as {
    id?: string;
    title?: string;
    tenantId?: string;
    summary?: { id?: string; title?: string; tenantId?: string; lastSeq?: bigint };
    lastSeq?: bigint;
  };
  const sid = s.id ?? s.summary?.id ?? id;
  const title = s.title ?? s.summary?.title ?? "Session";
  const tenantId = s.tenantId ?? s.summary?.tenantId ?? "";

  return (
    <div data-testid="session-detail">
      <PageHeader
        title={title || "Session"}
        description={sid}
        actions={<Badge variant="outline">session</Badge>}
      />

      <div className="mb-4 flex flex-wrap gap-3 text-sm">
        {tenantId ? (
          <Link className="text-primary hover:underline" to={`/tenants/${tenantId}`}>
            Tenant {tenantId.slice(0, 8)}…
          </Link>
        ) : null}
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/events", [{ field: "session_id", value: sid }])}
        >
          Events
        </Link>
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/runs", [{ field: "session_id", value: sid }])}
        >
          Runs
        </Link>
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/resources", [{ field: "session_id", value: sid }])}
        >
          Resources
        </Link>
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/memory", [{ field: "session_id", value: sid }])}
        >
          Memory
        </Link>
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/waits", [{ field: "session_id", value: sid }])}
        >
          Waits
        </Link>
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/automation", [{ field: "session_id", value: sid }])}
        >
          Automation
        </Link>
      </div>

      <div className="grid gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Diagnostic timeline</CardTitle>
          </CardHeader>
          <CardContent>
            {timeline.length === 0 ? (
              <div className="text-sm text-muted-foreground">No timeline entries.</div>
            ) : (
              <ul className="max-h-96 space-y-2 overflow-auto text-xs" data-testid="session-timeline">
                {timeline.map((entry, i) => {
                  const e = entry as {
                    kind?: string;
                    ts?: unknown;
                    refId?: string;
                    summary?: string;
                    actorDisplay?: string;
                  };
                  return (
                    <li key={i} className="rounded border border-border/60 px-2 py-1.5">
                      <div className="flex justify-between gap-2">
                        <span className="font-medium">{e.kind ?? "entry"}</span>
                        <span className="text-muted-foreground">{formatTs(e.ts as never)}</span>
                      </div>
                      <div className="text-muted-foreground">
                        {e.actorDisplay ? `${e.actorDisplay} · ` : ""}
                        {e.summary || e.refId || ""}
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}
          </CardContent>
        </Card>
        <JsonViewer value={session} title="Session detail" />
      </div>
    </div>
  );
}
