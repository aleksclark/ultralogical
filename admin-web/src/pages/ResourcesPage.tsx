import { useNavigate } from "react-router-dom";
import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import { EntityLink } from "@/components/EntityLink";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { Badge } from "@/components/ui";
import { formatTs } from "@/lib/format";
import type { ResourceSummary } from "@admin-gen/admin/v1/admin_pb";

function stateVariant(state: string): "success" | "warning" | "destructive" | "outline" {
  const s = state.toLowerCase();
  if (s === "ready" || s === "running") return "success";
  if (s === "failed" || s === "terminated") return "destructive";
  if (s === "pending" || s === "creating" || s === "reconciling") return "warning";
  return "outline";
}

const columns: ColumnDef<ResourceSummary>[] = [
  {
    id: "id",
    header: "Resource",
    width: 140,
    cell: (r) => <EntityLink kind="resource" id={r.id} />,
    getText: (r) => r.id,
  },
  {
    id: "kind",
    header: "Kind",
    sortable: true,
    width: 120,
    cell: (r) => r.kind,
  },
  {
    id: "state",
    header: "State",
    sortable: true,
    width: 110,
    cell: (r) => <Badge variant={stateVariant(r.state)}>{r.state}</Badge>,
  },
  {
    id: "provider_instance_id",
    header: "Provider",
    width: 130,
    cell: (r) =>
      r.providerInstanceId ? <EntityLink kind="provider" id={r.providerInstanceId} /> : "—",
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
    cell: (r) => <EntityLink kind="tenant" id={r.tenantId} />,
  },
  {
    id: "failure_message",
    header: "Failure",
    cell: (r) =>
      r.failureMessage ? <span className="text-xs text-destructive">{r.failureMessage}</span> : "—",
  },
  {
    id: "created_by_run_id",
    header: "Created by",
    width: 120,
    cell: (r) => (r.createdByRunId ? <EntityLink kind="run" id={r.createdByRunId} /> : "—"),
  },
  {
    id: "updated_at",
    header: "Updated",
    sortable: true,
    width: 170,
    cell: (r) => formatTs(r.updatedAt),
  },
];

const filters: FilterFieldMeta[] = [
  { name: "id", ops: ["eq", "in"] },
  { name: "kind", ops: ["eq", "in", "contains"] },
  { name: "state", ops: ["eq", "in", "ne"] },
  { name: "tenant_id", ops: ["eq", "in"] },
  { name: "session_id", ops: ["eq", "in"] },
  { name: "provider_instance_id", ops: ["eq", "in"] },
  { name: "created_by_run_id", ops: ["eq"] },
];

export function ResourcesPage() {
  const navigate = useNavigate();
  return (
    <CollectionPage<ResourceSummary>
      title="Resources"
      description="Resources by kind, provider, state, and session."
      collection="resources"
      columns={columns}
      filterFields={filters}
      rowKey={(r) => r.id}
      onRowNavigate={(r) => navigate(`/resources/${r.id}`)}
    />
  );
}
