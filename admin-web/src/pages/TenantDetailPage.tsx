import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { listFilterHref } from "@/components/EntityLink";
import { JsonViewer } from "@/components/JsonViewer";
import {
  Badge,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  ErrorState,
  PageHeader,
  Skeleton,
} from "@/components/ui";
import { fetchRelated, fetchTenant } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatTs } from "@/lib/format";

export function TenantDetailPage() {
  const { id = "" } = useParams();
  const client = useAdminClient();
  const [detail, setDetail] = useState<unknown>(null);
  const [related, setRelated] = useState<Record<string, unknown[]>>({});
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!id) return;
    const ac = new AbortController();
    setLoading(true);
    Promise.all([
      fetchTenant(client, id, ac.signal),
      fetchRelated(client, "tenants", id, "sessions", 20, ac.signal).catch(() => null),
      fetchRelated(client, "tenants", id, "api_keys", 20, ac.signal).catch(() => null),
      fetchRelated(client, "tenants", id, "providers", 20, ac.signal).catch(() => null),
    ])
      .then(([t, sessions, keys, providers]) => {
        setDetail(t.item ?? t);
        setRelated({
          sessions: sessions?.items ?? [],
          api_keys: keys?.items ?? [],
          providers: providers?.items ?? [],
        });
        setError(null);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
    return () => ac.abort();
  }, [client, id]);

  if (loading) return <Skeleton className="h-64 w-full" />;
  if (error) return <ErrorState message={error} />;

  const t = detail as {
    id?: string;
    name?: string;
    createdAt?: unknown;
    summary?: { name?: string; id?: string; createdAt?: unknown };
  };
  const name = t?.name ?? t?.summary?.name ?? id;
  const tid = t?.id ?? t?.summary?.id ?? id;

  return (
    <div data-testid="tenant-detail">
      <PageHeader
        title={name || "Tenant"}
        description={tid}
        actions={<Badge variant="outline">tenant</Badge>}
      />

      <div className="mb-4 flex flex-wrap gap-2 text-sm">
        <Link className="text-primary hover:underline" to={listFilterHref("/sessions", [{ field: "tenant_id", value: tid }])}>
          Sessions
        </Link>
        <Link className="text-primary hover:underline" to={listFilterHref("/runs", [{ field: "tenant_id", value: tid }])}>
          Runs
        </Link>
        <Link className="text-primary hover:underline" to={listFilterHref("/resources", [{ field: "tenant_id", value: tid }])}>
          Resources
        </Link>
        <Link className="text-primary hover:underline" to={listFilterHref("/api-keys", [{ field: "tenant_id", value: tid }])}>
          API keys
        </Link>
        <Link className="text-primary hover:underline" to={listFilterHref("/credentials", [{ field: "tenant_id", value: tid }])}>
          Credentials
        </Link>
        <Link className="text-primary hover:underline" to={`/sessions?tenant=${encodeURIComponent(tid)}`}>
          Scope all lists to tenant
        </Link>
      </div>

      <div className="grid gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Summary</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div>
              <span className="text-muted-foreground">Created </span>
              {formatTs((t.createdAt ?? t.summary?.createdAt) as never)}
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Related (first page)</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 text-xs">
            {Object.entries(related).map(([k, items]) => (
              <div key={k}>
                <div className="mb-1 font-medium capitalize">{k.replace("_", " ")}</div>
                <div className="text-muted-foreground">{items.length} item(s)</div>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>

      <div className="mt-4">
        <JsonViewer value={detail} title="Tenant detail" />
      </div>
    </div>
  );
}
