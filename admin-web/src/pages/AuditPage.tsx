import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { Badge } from "@/components/ui";
import { formatTs } from "@/lib/format";
import type { AuditEventSummary } from "@admin-gen/admin/v1/admin_pb";

const columns: ColumnDef<AuditEventSummary>[] = [
  {
    id: "ts",
    header: "When",
    sortable: true,
    width: 170,
    cell: (r) => formatTs(r.ts),
  },
  {
    id: "operator_id",
    header: "Operator",
    cell: (r) => (
      <span className="font-mono text-xs">
        {r.operatorId}
        <span className="text-muted-foreground"> ({r.operatorRole})</span>
      </span>
    ),
    getText: (r) => r.operatorId,
  },
  {
    id: "command",
    header: "Command",
    sortable: true,
    cell: (r) => <span className="font-mono text-xs">{r.command}</span>,
    getText: (r) => r.command,
  },
  {
    id: "result",
    header: "Result",
    sortable: true,
    width: 120,
    cell: (r) => (
      <Badge variant={r.result === "ok" || r.result === "dry_run" ? "success" : "destructive"}>
        {r.result}
      </Badge>
    ),
  },
  {
    id: "reason",
    header: "Reason",
    cell: (r) => <span className="truncate text-xs">{r.reason || "—"}</span>,
    getText: (r) => r.reason,
  },
  {
    id: "error",
    header: "Error",
    cell: (r) =>
      r.error ? <span className="text-xs text-destructive">{r.error}</span> : "—",
  },
];

const filters: FilterFieldMeta[] = [
  { name: "command", ops: ["eq", "contains", "prefix"] },
  { name: "result", ops: ["eq", "ne"] },
  { name: "operator_id", ops: ["eq", "contains", "prefix"] },
  { name: "operator_role", ops: ["eq"] },
];

export function AuditPage() {
  return (
    <div data-testid="audit-page">
      <CollectionPage<AuditEventSummary>
        title="Operator audit"
        description="Immutable admin command history. Operators cannot edit or delete these records."
        collection="audit_events"
        columns={columns}
        filterFields={filters}
        rowKey={(r) => r.id}
        searchPlaceholder="Search audit reasons / operators…"
        renderDetail={(_id, row) =>
          row ? (
            <pre className="max-h-[70vh] overflow-auto whitespace-pre-wrap font-mono text-[11px]">
              {JSON.stringify(row, null, 2)}
            </pre>
          ) : null
        }
      />
    </div>
  );
}
