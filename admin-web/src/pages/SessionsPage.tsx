import { useNavigate } from "react-router-dom";
import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import { EntityLink } from "@/components/EntityLink";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { Badge } from "@/components/ui";
import { formatCount, formatTs } from "@/lib/format";
import type { SessionSummary } from "@admin-gen/admin/v1/admin_pb";

const columns: ColumnDef<SessionSummary>[] = [
  {
    id: "title",
    header: "Title",
    sortable: true,
    cell: (r) => <EntityLink kind="session" id={r.id} label={r.title || "(untitled)"} mono={false} />,
    getText: (r) => r.title,
  },
  {
    id: "id",
    header: "ID",
    width: 140,
    cell: (r) => <EntityLink kind="session" id={r.id} />,
    getText: (r) => r.id,
  },
  {
    id: "tenant_id",
    header: "Tenant",
    width: 140,
    cell: (r) => <EntityLink kind="tenant" id={r.tenantId} />,
    getText: (r) => r.tenantId,
  },
  {
    id: "last_seq",
    header: "Last seq",
    width: 90,
    cell: (r) => formatCount(r.lastSeq),
  },
  {
    id: "event_count",
    header: "Events",
    width: 80,
    cell: (r) => formatCount(r.eventCount),
  },
  {
    id: "run_count",
    header: "Runs",
    width: 80,
    cell: (r) => formatCount(r.runCount),
  },
  {
    id: "resource_count",
    header: "Resources",
    width: 90,
    cell: (r) => formatCount(r.resourceCount),
  },
  {
    id: "archived_at",
    header: "Status",
    width: 100,
    cell: (r) =>
      r.archivedAt ? <Badge variant="muted">archived</Badge> : <Badge variant="success">active</Badge>,
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
  { name: "tenant_id", ops: ["eq", "in"] },
  { name: "title", ops: ["eq", "contains", "prefix"] },
  { name: "created_at", ops: ["gte", "lte"] },
];

export function SessionsPage() {
  const navigate = useNavigate();
  return (
    <CollectionPage<SessionSummary>
      title="Sessions"
      description="Searchable sessions with labels and activity counts."
      collection="sessions"
      columns={columns}
      filterFields={filters}
      rowKey={(r) => r.id}
      onRowNavigate={(r) => navigate(`/sessions/${r.id}`)}
    />
  );
}
