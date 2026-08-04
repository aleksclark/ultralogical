import { CommandConfirmModal } from "@/components/CommandConfirmModal";
import { useOperator } from "@/lib/operator";
import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { listFilterHref } from "@/components/EntityLink";
import { JsonViewer } from "@/components/JsonViewer";
import {
  Badge,
  Card,
  CardContent,
  ErrorState,
  PageHeader,
  Skeleton,
  Button,
} from "@/components/ui";
import { fetchProvider } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatTs } from "@/lib/format";

export function ProviderDetailPage() {
  const { can } = useOperator();
  const [cmdOpen, setCmdOpen] = useState(false);
  const { id = "" } = useParams();
  const client = useAdminClient();
  const [detail, setDetail] = useState<unknown>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!id) return;
    const ac = new AbortController();
    setLoading(true);
    fetchProvider(client, id, ac.signal)
      .then((res) => {
        setDetail(res.item ?? res);
        setError(null);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
    return () => ac.abort();
  }, [client, id]);

  if (loading) return <Skeleton className="h-64 w-full" />;
  if (error) return <ErrorState message={error} />;

  const p = detail as {
    id?: string;
    name?: string;
    kind?: string;
    state?: string;
    tenantId?: string;
    lastHealthyAt?: unknown;
    createdAt?: unknown;
    summary?: Record<string, unknown>;
  };
  const pid = p.id ?? id;
  const name = p.name ?? (p.summary?.name as string) ?? pid;

  return (
    <div data-testid="provider-detail">
      <PageHeader
        title={name}
        description={pid}
        actions={<Badge variant="outline">{p.state ?? "provider"}</Badge>}
      />
      <div className="mb-3 flex flex-wrap gap-2" data-testid="provider-actions">
        {can("ReprobeProvider") && (
          <Button size="sm" data-testid="action-reprobe-provider" onClick={() => setCmdOpen(true)}>Re-probe provider</Button>
        )}
      </div>
      {cmdOpen && (
        <CommandConfirmModal open onClose={() => setCmdOpen(false)} command="ReprobeProvider"
          args={{ providerId: id }} title="Re-probe provider" />
      )}

      <div className="mb-4 flex flex-wrap gap-3 text-sm">
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/resources", [{ field: "provider_instance_id", value: pid }])}
        >
          Resources
        </Link>
        <Link
          className="text-primary hover:underline"
          to={listFilterHref("/jobs", [{ field: "kind", op: "contains", value: "provider" }])}
        >
          Related jobs
        </Link>
        {p.tenantId ? (
          <Link className="text-primary hover:underline" to={`/tenants/${p.tenantId}`}>
            Tenant
          </Link>
        ) : null}
      </div>

      <Card className="mb-3">
        <CardContent className="grid gap-2 py-3 text-sm sm:grid-cols-2">
          <div>
            <span className="text-muted-foreground">Kind </span>
            {p.kind ?? "—"}
          </div>
          <div>
            <span className="text-muted-foreground">Last healthy </span>
            {formatTs(p.lastHealthyAt as never)}
          </div>
          <div>
            <span className="text-muted-foreground">Created </span>
            {formatTs(p.createdAt as never)}
          </div>
        </CardContent>
      </Card>

      <JsonViewer value={detail} title="Provider detail (config/capabilities)" />
    </div>
  );
}
