import { useNavigate } from "react-router-dom";
import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import { EntityLink } from "@/components/EntityLink";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { formatCount, formatTs } from "@/lib/format";
import type { TenantSummary } from "@admin-gen/admin/v1/admin_pb";

const columns: ColumnDef<TenantSummary>[] = [
  {
    id: "name",
    header: "Name",
    sortable: true,
    cell: (r) => <EntityLink kind="tenant" id={r.id} label={r.name} mono={false} />,
    getText: (r) => r.name,
  },
  {
    id: "id",
    header: "ID",
    sortable: true,
    width: 160,
    cell: (r) => <EntityLink kind="tenant" id={r.id} />,
    getText: (r) => r.id,
  },
  {
    id: "session_count",
    header: "Sessions",
    width: 100,
    cell: (r) => formatCount(r.sessionCount),
  },
  {
    id: "run_count",
    header: "Runs",
    width: 100,
    cell: (r) => formatCount(r.runCount),
  },
  {
    id: "api_key_count",
    header: "API keys",
    width: 100,
    cell: (r) => formatCount(r.apiKeyCount),
  },
  {
    id: "created_at",
    header: "Created",
    sortable: true,
    width: 180,
    cell: (r) => formatTs(r.createdAt),
  },
];

const filters: FilterFieldMeta[] = [
  { name: "name", ops: ["eq", "contains", "prefix"] },
  { name: "id", ops: ["eq", "in"] },
  { name: "created_at", ops: ["gte", "lte", "gt", "lt"] },
];

export function TenantsPage() {
  const navigate = useNavigate();
  return (
    <CollectionPage<TenantSummary>
      title="Tenants"
      description="Cross-tenant operator inventory."
      collection="tenants"
      columns={columns}
      filterFields={filters}
      rowKey={(r) => r.id}
      searchPlaceholder="Search tenants by name…"
      onRowNavigate={(r) => navigate(`/tenants/${r.id}`)}
    />
  );
}
