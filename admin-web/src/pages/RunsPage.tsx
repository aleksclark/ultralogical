import { useNavigate } from "react-router-dom";
import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import { EntityLink } from "@/components/EntityLink";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { Badge } from "@/components/ui";
import { formatTs } from "@/lib/format";
import type { RunSummary } from "@admin-gen/admin/v1/admin_pb";

function stateVariant(state: string): "success" | "warning" | "destructive" | "muted" | "outline" {
  const s = state.toLowerCase();
  if (s === "completed" || s === "succeeded") return "success";
  if (s === "failed" || s === "cancelled" || s === "canceled") return "destructive";
  if (s === "running" || s === "pending" || s === "waiting") return "warning";
  return "outline";
}

const columns: ColumnDef<RunSummary>[] = [
  {
    id: "id",
    header: "Run",
    width: 140,
    cell: (r) => <EntityLink kind="run" id={r.id} />,
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
    id: "session_id",
    header: "Session",
    width: 130,
    cell: (r) => <EntityLink kind="session" id={r.sessionId} />,
  },
  {
    id: "tenant_id",
    header: "Tenant",
    width: 120,
    cell: (r) => <EntityLink kind="tenant" id={r.tenantId} />,
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
    id: "failure_reason",
    header: "Failure",
    cell: (r) =>
      r.failureReason ? (
        <span className="text-xs text-destructive">{r.failureReason}</span>
      ) : (
        "—"
      ),
    getText: (r) => r.failureReason,
  },
  {
    id: "step_count",
    header: "Steps",
    width: 70,
    cell: (r) => String(r.stepCount),
  },
  {
    id: "parent_run_id",
    header: "Parent",
    width: 120,
    cell: (r) => (r.parentRunId ? <EntityLink kind="run" id={r.parentRunId} /> : "—"),
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
  { name: "session_id", ops: ["eq", "in"] },
  { name: "tenant_id", ops: ["eq", "in"] },
  { name: "actor_id", ops: ["eq", "contains", "prefix"] },
  { name: "actor_kind", ops: ["eq", "in"] },
  { name: "parent_run_id", ops: ["eq", "is_null", "is_not_null"] },
  { name: "failure_reason", ops: ["contains", "is_null", "is_not_null"] },
];

export function RunsPage() {
  const navigate = useNavigate();
  return (
    <CollectionPage<RunSummary>
      title="Runs"
      description="Agent runs by status, actor, session, and failure reason."
      collection="runs"
      columns={columns}
      filterFields={filters}
      rowKey={(r) => r.id}
      onRowNavigate={(r) => navigate(`/runs/${r.id}`)}
    />
  );
}
