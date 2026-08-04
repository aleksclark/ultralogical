import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { listFilterHref } from "@/components/EntityLink";
import {
  Badge,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  ErrorState,
  PageHeader,
  Skeleton,
  Stat,
} from "@/components/ui";
import { fetchRuntimeHealth } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatCount, formatTs } from "@/lib/format";
import type { RuntimeHealth } from "@admin-gen/admin/v1/admin_pb";

export function OverviewPage() {
  const client = useAdminClient();
  const [health, setHealth] = useState<RuntimeHealth | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const ac = new AbortController();
    setLoading(true);
    fetchRuntimeHealth(client, ac.signal)
      .then((res) => {
        setHealth(res.health ?? null);
        setError(null);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
    return () => ac.abort();
  }, [client]);

  if (loading) {
    return (
      <div className="grid gap-3 md:grid-cols-3">
        {Array.from({ length: 6 }).map((_, i) => (
          <Skeleton key={i} className="h-24" />
        ))}
      </div>
    );
  }

  if (error || !health) {
    return <ErrorState title="Failed to load runtime health" message={error} />;
  }

  const queueDepth =
    Number(health.queueAvailable) +
    Number(health.queueRunning) +
    Number(health.queueScheduled) +
    Number(health.queueRetryable);

  return (
    <div data-testid="overview-page">
      <PageHeader
        title="Overview"
        description="Runtime health, inventory counts, and shortcuts into problem views."
        actions={
          <Badge variant="outline" className="font-mono">
            schema {String(health.schemaVersion)} · {health.buildVersion || "unknown"}
          </Badge>
        }
      />

      <div className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Tenants" value={formatCount(health.tenantCount)} />
        <Stat label="Sessions" value={formatCount(health.sessionCount)} />
        <Stat label="Runs" value={formatCount(health.runCount)} />
        <Stat label="Events" value={formatCount(health.eventCount)} />
        <Stat label="Resources" value={formatCount(health.resourceCount)} />
        <Stat label="Providers" value={formatCount(health.providerCount)} />
        <Stat
          label="Queue depth"
          value={formatCount(queueDepth)}
          tone={queueDepth > 0 ? "warning" : "default"}
          hint={
            <span>
              avail {formatCount(health.queueAvailable)} · run {formatCount(health.queueRunning)} ·
              retry {formatCount(health.queueRetryable)} · disc {formatCount(health.queueDiscarded)}
            </span>
          }
        />
        <Stat
          label="Open waits"
          value={formatCount(health.openWaitCount)}
          tone={Number(health.openWaitCount) > 0 ? "warning" : "default"}
        />
      </div>

      <div className="grid gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Problem views</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <ProblemLink
              to={listFilterHref("/runs", [{ field: "state", value: "failed" }])}
              label="Failed runs"
              detail="Investigate terminal failures and step history"
            />
            <ProblemLink
              to={listFilterHref("/jobs", [{ field: "state", value: "retryable" }])}
              label="Retryable jobs"
              detail="Queue pressure and worker errors"
            />
            <ProblemLink
              to={listFilterHref("/jobs", [], { s: "scheduled_at" })}
              label="Oldest scheduled jobs"
              detail="Latency spike workflow entry"
            />
            <ProblemLink
              to={listFilterHref("/resources", [{ field: "state", value: "failed" }])}
              label="Failed resources"
              detail="Stuck lifecycle → provider → job"
            />
            <ProblemLink
              to="/security"
              label="Actor / security diagnostics"
              detail="Search actors across events and runs"
            />
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Runtime</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <Row k="Server time" v={formatTs(health.serverTime)} />
            <Row k="River schema" v={health.riverSchemaPresent ? "present" : "absent"} />
            <Row k="Subscriber hint" v={formatCount(health.activeSubscriberHint)} />
            <Row
              k="Diagnostics"
              v={
                Object.keys(health.diagnostics ?? {}).length
                  ? Object.entries(health.diagnostics)
                      .map(([k, v]) => `${k}=${v}`)
                      .join(", ")
                  : "—"
              }
            />
            <div className="pt-2">
              <Link className="text-primary hover:underline" to="/internals">
                Open internals →
              </Link>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function ProblemLink({ to, label, detail }: { to: string; label: string; detail: string }) {
  return (
    <Link
      to={to}
      className="block rounded-md border border-transparent px-2 py-2 hover:border-border hover:bg-accent/40"
    >
      <div className="font-medium text-primary">{label}</div>
      <div className="text-xs text-muted-foreground">{detail}</div>
    </Link>
  );
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex justify-between gap-3">
      <span className="text-muted-foreground">{k}</span>
      <span className="text-right font-mono text-xs">{v}</span>
    </div>
  );
}
