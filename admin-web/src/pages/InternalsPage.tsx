import { useEffect, useState } from "react";
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
import { fetchCollections, fetchRuntimeHealth } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatCount, formatTs } from "@/lib/format";

const BUILD_SHA = (import.meta.env.VITE_BUILD_SHA as string | undefined) || "dev";

export function InternalsPage() {
  const client = useAdminClient();
  const [health, setHealth] = useState<unknown>(null);
  const [collections, setCollections] = useState<unknown>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const ac = new AbortController();
    setLoading(true);
    Promise.all([
      fetchRuntimeHealth(client, ac.signal),
      fetchCollections(client, ac.signal),
    ])
      .then(([h, c]) => {
        setHealth(h.health ?? h);
        setCollections(c.collections ?? c);
        setError(null);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
    return () => ac.abort();
  }, [client]);

  if (loading) return <Skeleton className="h-64 w-full" />;
  if (error) return <ErrorState message={error} />;

  const h = health as {
    buildVersion?: string;
    schemaVersion?: bigint;
    riverSchemaPresent?: boolean;
    serverTime?: unknown;
    tenantCount?: bigint;
    diagnostics?: Record<string, string>;
  };

  return (
    <div data-testid="internals-page">
      <PageHeader
        title="Internals"
        description="Build, schema, collection descriptors, and raw health snapshot."
        actions={<Badge variant="outline">SPA {BUILD_SHA}</Badge>}
      />

      <div className="mb-4 grid gap-3 md:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle>Build</CardTitle>
          </CardHeader>
          <CardContent className="space-y-1 text-sm">
            <div>
              API <span className="font-mono text-xs">{h.buildVersion || "unknown"}</span>
            </div>
            <div>
              SPA <span className="font-mono text-xs">{BUILD_SHA}</span>
            </div>
            <div>
              Schema <span className="font-mono text-xs">{String(h.schemaVersion ?? "—")}</span>
            </div>
            <div>River schema {h.riverSchemaPresent ? "present" : "absent"}</div>
            <div className="text-muted-foreground">{formatTs(h.serverTime as never)}</div>
          </CardContent>
        </Card>
        <Card className="md:col-span-2">
          <CardHeader>
            <CardTitle>Inventory counts</CardTitle>
          </CardHeader>
          <CardContent className="text-sm">
            <div>Tenants: {formatCount(h.tenantCount)}</div>
            <div className="mt-2 text-xs text-muted-foreground">
              Full counts are on the overview page. Schema table inventory expansion is E7.
            </div>
            {h.diagnostics && Object.keys(h.diagnostics).length ? (
              <pre className="mt-2 overflow-auto rounded border bg-background/50 p-2 font-mono text-[11px]">
                {Object.entries(h.diagnostics)
                  .map(([k, v]) => `${k}=${v}`)
                  .join("\n")}
              </pre>
            ) : null}
          </CardContent>
        </Card>
      </div>

      <div className="space-y-3">
        <JsonViewer value={health} title="Runtime health (raw)" />
        <JsonViewer value={collections} title="Collection descriptors" defaultCollapsed />
      </div>
    </div>
  );
}
