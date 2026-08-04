import { CommandConfirmModal } from "@/components/CommandConfirmModal";
import { useOperator } from "@/lib/operator";
import { useEffect, useState } from "react";
import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import { EntityLink } from "@/components/EntityLink";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { JsonViewer } from "@/components/JsonViewer";
import { Badge, Button, Skeleton } from "@/components/ui";
import { fetchAPIKey } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatTs } from "@/lib/format";
import type { APIKeySummary } from "@admin-gen/admin/v1/admin_pb";

const columns: ColumnDef<APIKeySummary>[] = [
  {
    id: "name",
    header: "Name",
    sortable: true,
    cell: (r) => r.name,
    getText: (r) => r.name,
  },
  {
    id: "id",
    header: "ID",
    width: 130,
    cell: (r) => <span className="font-mono text-xs">{r.id.slice(0, 8)}…</span>,
    getText: (r) => r.id,
  },
  {
    id: "tenant_id",
    header: "Tenant",
    width: 130,
    cell: (r) => <EntityLink kind="tenant" id={r.tenantId} />,
  },
  {
    id: "prefix",
    header: "Prefix",
    width: 120,
    cell: (r) => <span className="font-mono text-xs">{r.prefix}</span>,
  },
  {
    id: "scope",
    header: "Scope",
    width: 100,
    cell: (r) => r.scope,
  },
  {
    id: "revoked_at",
    header: "Status",
    width: 100,
    cell: (r) =>
      r.revokedAt ? <Badge variant="destructive">revoked</Badge> : <Badge variant="success">active</Badge>,
  },
  {
    id: "key_hash_prefix",
    header: "Hash prefix",
    width: 110,
    cell: (r) => <span className="font-mono text-[11px]">{r.keyHashPrefix}</span>,
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
  { name: "name", ops: ["eq", "contains", "prefix"] },
  { name: "scope", ops: ["eq", "in"] },
  { name: "prefix", ops: ["eq", "prefix", "contains"] },
];

function KeyDetail({ id }: { id: string }) {
  const client = useAdminClient();
  const [detail, setDetail] = useState<unknown>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    fetchAPIKey(client, id, ac.signal)
      .then((res) => setDetail(res.item ?? res))
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)));
    return () => ac.abort();
  }, [client, id]);

  if (err) return <div className="text-sm text-destructive">{err}</div>;
  if (!detail) return <Skeleton className="h-32 w-full" />;
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground">
        Metadata only — never raw key material, full hash, or ciphertext plaintext.
      </p>
      <JsonViewer value={detail} title="API key metadata" />
    </div>
  );
}

export function APIKeysPage() {
  const { can } = useOperator();
  const [revokeId, setRevokeId] = useState<string | null>(null);

  return (
    <>
      {revokeId && (
        <CommandConfirmModal open onClose={() => setRevokeId(null)} command="RevokeAPIKey"
          args={{ apiKeyId: revokeId }} title="Revoke API key" />
      )}
      <CollectionPage<APIKeySummary>
      title="API keys"
      description="Tenant API key metadata and revocation status. Raw keys are never shown."
      collection="api_keys"
      toolbarExtra={can("RevokeAPIKey") ? (
        <Button size="sm" variant="outline" data-testid="action-revoke-key" onClick={() => {
          const id = window.prompt("API key id to revoke");
          if (id?.trim()) setRevokeId(id.trim());
        }}>Revoke key…</Button>
      ) : null}
      columns={columns}
      filterFields={filters}
      rowKey={(r) => r.id}
      detailTitle={(id) => `API key ${id.slice(0, 8)}…`}
      renderDetail={(id) => <KeyDetail id={id} />}
    />
    </>
  );
}
