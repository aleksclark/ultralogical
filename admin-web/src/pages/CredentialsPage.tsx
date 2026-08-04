import { CommandConfirmModal } from "@/components/CommandConfirmModal";
import { useOperator } from "@/lib/operator";
import { useEffect, useState } from "react";
import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import { EntityLink } from "@/components/EntityLink";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { JsonViewer } from "@/components/JsonViewer";
import { Badge, Button, Skeleton } from "@/components/ui";
import { fetchCredential } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatBytes, formatTs } from "@/lib/format";
import type { CredentialSummary } from "@admin-gen/admin/v1/admin_pb";

const columns: ColumnDef<CredentialSummary>[] = [
  {
    id: "name",
    header: "Name",
    sortable: true,
    cell: (r) => r.name,
    getText: (r) => r.name,
  },
  {
    id: "kind",
    header: "Kind",
    sortable: true,
    width: 120,
    cell: (r) => r.kind,
  },
  {
    id: "tenant_id",
    header: "Tenant",
    width: 130,
    cell: (r) => <EntityLink kind="tenant" id={r.tenantId} />,
  },
  {
    id: "encrypted",
    header: "Encrypted",
    width: 100,
    cell: (r) => (
      <Badge variant={r.encrypted ? "success" : "warning"}>{r.encrypted ? "yes" : "no"}</Badge>
    ),
  },
  {
    id: "ciphertext_bytes",
    header: "Ciphertext",
    width: 110,
    cell: (r) => formatBytes(r.ciphertextBytes),
  },
  {
    id: "created_at",
    header: "Created",
    sortable: true,
    width: 170,
    cell: (r) => formatTs(r.createdAt),
  },
  {
    id: "rotated_at",
    header: "Rotated",
    width: 170,
    cell: (r) => formatTs(r.rotatedAt),
  },
];

const filters: FilterFieldMeta[] = [
  { name: "tenant_id", ops: ["eq", "in"] },
  { name: "kind", ops: ["eq", "in"] },
  { name: "name", ops: ["eq", "contains", "prefix"] },
];

function credKey(r: CredentialSummary) {
  return `${r.tenantId}/${r.kind}/${r.name}`;
}

function CredentialDetail({ id, row }: { id: string; row?: CredentialSummary }) {
  const client = useAdminClient();
  const [detail, setDetail] = useState<unknown>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    let tenantId = row?.tenantId;
    let kind = row?.kind;
    let name = row?.name;
    if ((!tenantId || !kind || !name) && id.includes("/")) {
      const [t, k, ...rest] = id.split("/");
      tenantId = t;
      kind = k;
      name = rest.join("/");
    }
    if (!tenantId || !kind || !name) {
      setErr("Need tenant/kind/name");
      return;
    }
    fetchCredential(client, tenantId, kind, name, ac.signal)
      .then((res) => setDetail(res.item ?? res))
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)));
    return () => ac.abort();
  }, [client, id, row]);

  if (err) return <div className="text-sm text-destructive">{err}</div>;
  if (!detail) return <Skeleton className="h-32 w-full" />;
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground">
        Metadata only — ciphertext length and redaction status. Plaintext never leaves the server.
      </p>
      <JsonViewer value={detail} title="Credential metadata" />
    </div>
  );
}

export function CredentialsPage() {
  const { can, operator } = useOperator();
  const [disable, setDisable] = useState<{ tenantId: string; kind: string; name: string } | null>(null);
  const [reveal, setReveal] = useState<{ tenantId: string; kind: string; name: string } | null>(null);

  return (
    <>
      {disable && (
        <CommandConfirmModal open onClose={() => setDisable(null)} command="DisableCredential"
          args={{ tenantId: disable.tenantId, kind: disable.kind, name: disable.name }}
          title="Disable credential" confirmPhrase="disable" />
      )}
      {reveal && (
        <CommandConfirmModal open onClose={() => setReveal(null)} command="RevealSecret"
          args={{ secretKind: "credential", tenantId: reveal.tenantId, credentialKind: reveal.kind, credentialName: reveal.name }}
          title="Break-glass reveal credential" requireReauth />
      )}
      <CollectionPage<CredentialSummary>
      title="Credentials"
      description="Encrypted credential metadata only. Plaintext is never returned."
      collection="credentials"
      toolbarExtra={<div className="flex gap-2">
        {can("DisableCredential") && (
          <Button size="sm" variant="outline" data-testid="action-disable-credential" onClick={() => {
            const tenantId = window.prompt("Tenant id")?.trim();
            const kind = window.prompt("Credential kind")?.trim();
            const name = window.prompt("Credential name")?.trim();
            if (tenantId && kind && name) setDisable({ tenantId, kind, name });
          }}>Disable…</Button>
        )}
        {can("RevealSecret") && operator?.revealEnabled && (
          <Button size="sm" variant="destructive" data-testid="action-reveal-credential" onClick={() => {
            const tenantId = window.prompt("Tenant id")?.trim();
            const kind = window.prompt("Credential kind")?.trim();
            const name = window.prompt("Credential name")?.trim();
            if (tenantId && kind && name) setReveal({ tenantId, kind, name });
          }}>Reveal…</Button>
        )}
      </div>}
      columns={columns}
      filterFields={filters}
      rowKey={credKey}
      detailTitle={(id) => `Credential ${id}`}
      renderDetail={(id, row) => <CredentialDetail id={id} row={row} />}
    />
    </>
  );
}
