import { useEffect, useState } from "react";
import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import { EntityLink } from "@/components/EntityLink";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { JsonViewer } from "@/components/JsonViewer";
import { Skeleton } from "@/components/ui";
import { fetchMemory } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatBytes, formatTs } from "@/lib/format";
import type { MemorySummary } from "@admin-gen/admin/v1/admin_pb";

const columns: ColumnDef<MemorySummary>[] = [
  {
    id: "key",
    header: "Key",
    sortable: true,
    cell: (r) => <span className="font-mono text-xs">{r.key}</span>,
    getText: (r) => r.key,
  },
  {
    id: "session_id",
    header: "Session",
    width: 130,
    cell: (r) => <EntityLink kind="session" id={r.sessionId} />,
    getText: (r) => r.sessionId,
  },
  {
    id: "tenant_id",
    header: "Tenant",
    width: 120,
    cell: (r) => <EntityLink kind="tenant" id={r.tenantId} />,
  },
  {
    id: "updated_by_id",
    header: "Updated by",
    cell: (r) => (
      <span className="text-xs">
        <span className="text-muted-foreground">{r.updatedByKind}/</span>
        {r.updatedById || "—"}
      </span>
    ),
    getText: (r) => `${r.updatedByKind}:${r.updatedById}`,
  },
  {
    id: "value_bytes",
    header: "Size",
    width: 90,
    cell: (r) => formatBytes(r.valueBytes),
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
  { name: "session_id", ops: ["eq", "in"] },
  { name: "tenant_id", ops: ["eq", "in"] },
  { name: "key", ops: ["eq", "contains", "prefix"] },
  { name: "updated_by_id", ops: ["eq", "contains", "prefix"] },
  { name: "updated_by_kind", ops: ["eq", "in"] },
];

function memoryKey(r: MemorySummary) {
  return `${r.sessionId}:${r.key}`;
}

function MemoryDetail({ id, row }: { id: string; row?: MemorySummary }) {
  const client = useAdminClient();
  const [detail, setDetail] = useState<unknown>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    let sessionId = row?.sessionId;
    let key = row?.key;
    if ((!sessionId || !key) && id.includes(":")) {
      const idx = id.indexOf(":");
      sessionId = id.slice(0, idx);
      key = id.slice(idx + 1);
    }
    if (!sessionId || !key) {
      setErr("Need session_id:key");
      return;
    }
    fetchMemory(client, sessionId, key, ac.signal)
      .then((res) => setDetail(res.item ?? res))
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)));
    return () => ac.abort();
  }, [client, id, row]);

  if (err) return <div className="text-sm text-destructive">{err}</div>;
  if (!detail) return <Skeleton className="h-32 w-full" />;
  return (
    <div className="space-y-2" data-testid="memory-detail">
      <p className="text-xs text-muted-foreground">
        Session memory entry metadata and value payload as returned by admin.v1 (no secrets).
      </p>
      <JsonViewer value={detail} title="Memory entry" />
    </div>
  );
}

export function MemoryPage() {
  return (
    <CollectionPage<MemorySummary>
      title="Memory"
      description="Session memory entries by session, key, and updater."
      collection="memory"
      columns={columns}
      filterFields={filters}
      rowKey={memoryKey}
      searchPlaceholder="Search memory keys…"
      detailTitle={(id) => `Memory ${id}`}
      renderDetail={(id, row) => <MemoryDetail id={id} row={row} />}
    />
  );
}
