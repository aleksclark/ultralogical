import { useNavigate } from "react-router-dom";
import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import { EntityLink } from "@/components/EntityLink";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { Badge } from "@/components/ui";
import { formatTs } from "@/lib/format";
import type { JobSummary } from "@admin-gen/admin/v1/admin_pb";

function stateVariant(state: string): "success" | "warning" | "destructive" | "outline" | "muted" {
  const s = state.toLowerCase();
  if (s === "completed") return "success";
  if (s === "discarded" || s === "cancelled") return "destructive";
  if (s === "retryable" || s === "running" || s === "available" || s === "scheduled") return "warning";
  return "outline";
}

const columns: ColumnDef<JobSummary>[] = [
  {
    id: "id",
    header: "Job",
    width: 100,
    cell: (r) => <EntityLink kind="job" id={String(r.id)} label={String(r.id)} />,
    getText: (r) => String(r.id),
  },
  {
    id: "kind",
    header: "Kind",
    sortable: true,
    cell: (r) => <span className="font-mono text-xs">{r.kind}</span>,
    getText: (r) => r.kind,
  },
  {
    id: "state",
    header: "State",
    sortable: true,
    width: 110,
    cell: (r) => <Badge variant={stateVariant(r.state)}>{r.state}</Badge>,
  },
  {
    id: "attempt",
    header: "Attempt",
    width: 90,
    cell: (r) => `${r.attempt}/${r.maxAttempts}`,
  },
  {
    id: "queue",
    header: "Queue",
    width: 100,
    cell: (r) => r.queue || "—",
  },
  {
    id: "errors_preview",
    header: "Errors",
    cell: (r) =>
      r.errorsPreview ? (
        <span className="text-xs text-destructive">{r.errorsPreview}</span>
      ) : (
        "—"
      ),
    getText: (r) => r.errorsPreview,
  },
  {
    id: "scheduled_at",
    header: "Scheduled",
    sortable: true,
    width: 170,
    cell: (r) => formatTs(r.scheduledAt),
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
  { name: "kind", ops: ["eq", "in", "contains", "prefix"] },
  { name: "state", ops: ["eq", "in", "ne"] },
  { name: "queue", ops: ["eq", "in"] },
  { name: "attempt", ops: ["eq", "gte", "lte"] },
];

export function JobsPage() {
  const navigate = useNavigate();
  return (
    <CollectionPage<JobSummary>
      title="Jobs"
      description="River queue jobs by kind, state, attempt, and schedule."
      collection="jobs"
      columns={columns}
      filterFields={filters}
      rowKey={(r) => String(r.id)}
      onRowNavigate={(r) => navigate(`/jobs/${r.id}`)}
    />
  );
}
