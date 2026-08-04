import { useNavigate } from "react-router-dom";
import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import { EntityLink } from "@/components/EntityLink";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { Badge } from "@/components/ui";
import { formatCount, formatTs } from "@/lib/format";
import type { ProviderSummary } from "@admin-gen/admin/v1/admin_pb";

const columns: ColumnDef<ProviderSummary>[] = [
  {
    id: "name",
    header: "Name",
    sortable: true,
    cell: (r) => <EntityLink kind="provider" id={r.id} label={r.name || r.id} mono={false} />,
    getText: (r) => r.name,
  },
  {
    id: "id",
    header: "ID",
    width: 130,
    cell: (r) => <EntityLink kind="provider" id={r.id} />,
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
    cell: (r) => <Badge variant="outline">{r.state}</Badge>,
  },
  {
    id: "tenant_id",
    header: "Tenant",
    width: 120,
    cell: (r) => <EntityLink kind="tenant" id={r.tenantId} />,
  },
  {
    id: "resource_count",
    header: "Resources",
    width: 100,
    cell: (r) => formatCount(r.resourceCount),
  },
  {
    id: "last_healthy_at",
    header: "Last healthy",
    sortable: true,
    width: 170,
    cell: (r) => formatTs(r.lastHealthyAt),
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
  { name: "kind", ops: ["eq", "in"] },
  { name: "state", ops: ["eq", "in", "ne"] },
  { name: "tenant_id", ops: ["eq", "in"] },
  { name: "name", ops: ["eq", "contains", "prefix"] },
];

export function ProvidersPage() {
  const navigate = useNavigate();
  return (
    <CollectionPage<ProviderSummary>
      title="Providers"
      description="Provider registrations, health, and resource counts."
      collection="providers"
      columns={columns}
      filterFields={filters}
      rowKey={(r) => r.id}
      onRowNavigate={(r) => navigate(`/providers/${r.id}`)}
    />
  );
}
