import { useEffect, useState } from "react";
import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import { EntityLink } from "@/components/EntityLink";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { JsonViewer } from "@/components/JsonViewer";
import { Badge, Skeleton } from "@/components/ui";
import { fetchWait } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatTs } from "@/lib/format";
import type { WaitSummary } from "@admin-gen/admin/v1/admin_pb";

function stateVariant(state: string): "success" | "warning" | "destructive" | "outline" | "muted" {
  const s = state.toLowerCase();
  if (s === "resolved" || s === "resumed" || s === "completed") return "success";
  if (s === "open" || s === "waiting" || s === "pending") return "warning";
  if (s === "timed_out" || s === "failed" || s === "cancelled" || s === "canceled") {
    return "destructive";
  }
  return "outline";
}

const columns: ColumnDef<WaitSummary>[] = [
  {
    id: "id",
    header: "Wait",
    width: 130,
    cell: (r) => <span className="font-mono text-xs">{r.id.slice(0, 8)}…</span>,
    getText: (r) => r.id,
  },
  {
    id: "state",
    header: "State",
    sortable: true,
    width: 110,
    cell: (r) => <Badge variant={stateVariant(r.state)}>{r.state}</Badge>,
    getText: (r) => r.state,
  },
  {
    id: "kind",
    header: "Kind",
    sortable: true,
    width: 120,
    cell: (r) => r.kind || "—",
    getText: (r) => r.kind,
  },
  {
    id: "parent_run_id",
    header: "Parent run",
    width: 130,
    cell: (r) =>
      r.parentRunId ? <EntityLink kind="run" id={r.parentRunId} /> : "—",
    getText: (r) => r.parentRunId,
  },
  {
    id: "session_id",
    header: "Session",
    width: 120,
    cell: (r) => (r.sessionId ? <EntityLink kind="session" id={r.sessionId} /> : "—"),
  },
  {
    id: "tenant_id",
    header: "Tenant",
    width: 120,
    cell: (r) => (r.tenantId ? <EntityLink kind="tenant" id={r.tenantId} /> : "—"),
  },
  {
    id: "step_index",
    header: "Step",
    width: 70,
    cell: (r) => String(r.stepIndex),
  },
  {
    id: "deadline",
    header: "Deadline",
    sortable: true,
    width: 170,
    cell: (r) => formatTs(r.deadline),
  },
  {
    id: "created_at",
    header: "Created",
    sortable: true,
    width: 170,
    cell: (r) => formatTs(r.createdAt),
  },
];

const filters: FilterFieldMeta[] = [
  { name: "id", ops: ["eq", "in"] },
  { name: "state", ops: ["eq", "in", "ne"] },
  { name: "kind", ops: ["eq", "in", "contains"] },
  { name: "parent_run_id", ops: ["eq", "in"] },
  { name: "session_id", ops: ["eq", "in"] },
  { name: "tenant_id", ops: ["eq", "in"] },
  { name: "tool_call_id", ops: ["eq", "contains"] },
];

function WaitDetail({ id }: { id: string }) {
  const client = useAdminClient();
  const [detail, setDetail] = useState<unknown>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    fetchWait(client, id, ac.signal)
      .then((res) => setDetail(res.item ?? res))
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)));
    return () => ac.abort();
  }, [client, id]);

  if (err) return <div className="text-sm text-destructive">{err}</div>;
  if (!detail) return <Skeleton className="h-32 w-full" />;

  const d = detail as {
    parentRunId?: string;
    sessionId?: string;
    toolCallId?: string;
    state?: string;
  };

  return (
    <div className="space-y-3" data-testid="wait-detail">
      <div className="space-y-1 text-sm">
        {d.parentRunId ? (
          <div>
            Parent run: <EntityLink kind="run" id={d.parentRunId} />
          </div>
        ) : null}
        {d.sessionId ? (
          <div>
            Session: <EntityLink kind="session" id={d.sessionId} />
          </div>
        ) : null}
        {d.toolCallId ? (
          <div className="text-muted-foreground">
            Tool call <span className="font-mono text-xs">{d.toolCallId}</span>
          </div>
        ) : null}
      </div>
      <JsonViewer value={detail} title="Wait detail" />
    </div>
  );
}

export function WaitsPage() {
  return (
    <CollectionPage<WaitSummary>
      title="Waits"
      description="Spawn/wait and tool await state correlated to parent runs and sessions."
      collection="waits"
      columns={columns}
      filterFields={filters}
      rowKey={(r) => r.id}
      searchPlaceholder="Search waits…"
      detailTitle={(id) => `Wait ${id.slice(0, 8)}…`}
      renderDetail={(id) => <WaitDetail id={id} />}
    />
  );
}
