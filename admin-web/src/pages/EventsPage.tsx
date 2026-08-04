import { useEffect, useState } from "react";
import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import { EntityLink } from "@/components/EntityLink";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { JsonViewer } from "@/components/JsonViewer";
import { Badge, Skeleton } from "@/components/ui";
import { fetchEvent } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatBytes, formatTs } from "@/lib/format";
import type { EventSummary } from "@admin-gen/admin/v1/admin_pb";

const columns: ColumnDef<EventSummary>[] = [
  {
    id: "ts",
    header: "Time",
    sortable: true,
    width: 170,
    cell: (r) => formatTs(r.ts),
  },
  {
    id: "session_id",
    header: "Session",
    width: 130,
    cell: (r) => <EntityLink kind="session" id={r.sessionId} />,
    getText: (r) => r.sessionId,
  },
  {
    id: "seq",
    header: "Seq",
    width: 70,
    cell: (r) => String(r.seq),
  },
  {
    id: "kind",
    header: "Kind",
    sortable: true,
    width: 140,
    cell: (r) => <Badge variant="outline">{r.kind}</Badge>,
    getText: (r) => r.kind,
  },
  {
    id: "actor_id",
    header: "Actor",
    cell: (r) => (
      <span className="text-xs">
        <span className="text-muted-foreground">{r.actorKind}/</span>
        {r.actorDisplay || r.actorId || "—"}
      </span>
    ),
    getText: (r) => `${r.actorKind}:${r.actorId}`,
  },
  {
    id: "tenant_id",
    header: "Tenant",
    width: 120,
    cell: (r) => <EntityLink kind="tenant" id={r.tenantId} />,
  },
  {
    id: "payload_bytes",
    header: "Payload",
    width: 90,
    cell: (r) => formatBytes(r.payloadBytes),
  },
  {
    id: "payload_preview",
    header: "Preview",
    cell: (r) => <span className="font-mono text-[11px] text-muted-foreground">{r.payloadPreview}</span>,
    getText: (r) => r.payloadPreview,
  },
];

const filters: FilterFieldMeta[] = [
  { name: "session_id", ops: ["eq", "in"] },
  { name: "tenant_id", ops: ["eq", "in"] },
  { name: "kind", ops: ["eq", "in", "contains", "prefix"] },
  { name: "actor_id", ops: ["eq", "contains", "prefix"] },
  { name: "actor_kind", ops: ["eq", "in"] },
  { name: "seq", ops: ["eq", "gte", "lte"] },
];

function EventDetail({ id, row }: { id: string; row?: EventSummary }) {
  const client = useAdminClient();
  const [detail, setDetail] = useState<unknown>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    // detail key is sessionId:seq
    let sessionId = row?.sessionId;
    let seq = row?.seq;
    if ((!sessionId || seq === undefined) && id.includes(":")) {
      const [s, n] = id.split(":");
      sessionId = s;
      seq = BigInt(n);
    }
    if (!sessionId || seq === undefined) {
      setErr("Need session_id:seq");
      return;
    }
    fetchEvent(client, sessionId, seq, ac.signal)
      .then((res) => setDetail(res.item ?? res))
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)));
    return () => ac.abort();
  }, [client, id, row]);

  if (err) return <div className="text-sm text-destructive">{err}</div>;
  if (!detail) return <Skeleton className="h-40 w-full" />;

  const d = detail as {
    actorKind?: string;
    actorId?: string;
    actorDisplay?: string;
    kind?: string;
    sessionId?: string;
  };

  return (
    <div className="space-y-3" data-testid="event-detail">
      <div className="text-sm">
        <div>
          Actor:{" "}
          <span className="font-mono">
            {d.actorKind}/{d.actorDisplay || d.actorId}
          </span>
        </div>
        {d.sessionId ? (
          <div className="mt-1">
            Session: <EntityLink kind="session" id={d.sessionId} />
          </div>
        ) : null}
        {d.actorId ? (
          <div className="mt-1">
            Related runs:{" "}
            <EntityLink
              kind="run"
              id={d.actorId}
              label="open if actor is run"
              mono={false}
            />{" "}
            ·{" "}
            <a
              className="text-primary hover:underline"
              href={`/runs?f=${encodeURIComponent(`actor_id:eq:${d.actorId}`)}`}
            >
              filter runs by actor
            </a>
          </div>
        ) : null}
      </div>
      <JsonViewer value={detail} title="Event payload" />
    </div>
  );
}

export function EventsPage() {
  return (
    <CollectionPage<EventSummary>
      title="Events"
      description="Global and session-scoped event stream (bounded pages)."
      collection="events"
      columns={columns}
      filterFields={filters}
      rowKey={(r) => `${r.sessionId}:${r.seq}`}
      searchPlaceholder="Search events…"
      detailTitle={(id) => `Event ${id}`}
      renderDetail={(id, row) => <EventDetail id={id} row={row} />}
    />
  );
}
