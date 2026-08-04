import { useEffect, useState } from "react";
import type { ColumnDef } from "@/components/AdminDataTable";
import { CollectionPage } from "@/components/CollectionPage";
import { EntityLink } from "@/components/EntityLink";
import type { FilterFieldMeta } from "@/components/FilterBuilder";
import { JsonViewer } from "@/components/JsonViewer";
import { Badge, Skeleton } from "@/components/ui";
import { fetchPeriodicPrompt } from "@/data/details";
import { useAdminClient } from "@/lib/client";
import { formatBytes, formatTs } from "@/lib/format";
import type { PeriodicPromptSummary } from "@admin-gen/admin/v1/admin_pb";

const columns: ColumnDef<PeriodicPromptSummary>[] = [
  {
    id: "id",
    header: "ID",
    width: 130,
    cell: (r) => <span className="font-mono text-xs">{r.id.slice(0, 8)}…</span>,
    getText: (r) => r.id,
  },
  {
    id: "schedule",
    header: "Schedule",
    sortable: true,
    cell: (r) => <span className="font-mono text-xs">{r.schedule}</span>,
  },
  {
    id: "enabled",
    header: "Enabled",
    width: 90,
    cell: (r) => (
      <Badge variant={r.enabled ? "success" : "muted"}>{r.enabled ? "yes" : "no"}</Badge>
    ),
  },
  {
    id: "session_id",
    header: "Session",
    width: 120,
    cell: (r) => (r.sessionId ? <EntityLink kind="session" id={r.sessionId} /> : "—"),
  },
  {
    id: "run_id",
    header: "Run",
    width: 120,
    cell: (r) => (r.runId ? <EntityLink kind="run" id={r.runId} /> : "—"),
  },
  {
    id: "tenant_id",
    header: "Tenant",
    width: 120,
    cell: (r) => <EntityLink kind="tenant" id={r.tenantId} />,
  },
  {
    id: "next_at",
    header: "Next fire",
    sortable: true,
    width: 170,
    cell: (r) => formatTs(r.nextAt),
  },
  {
    id: "prompt_preview",
    header: "Preview",
    cell: (r) => (
      <span className="font-mono text-[11px] text-muted-foreground">
        {r.promptPreview || formatBytes(r.promptBytes)}
      </span>
    ),
  },
];

const filters: FilterFieldMeta[] = [
  { name: "id", ops: ["eq", "in"] },
  { name: "tenant_id", ops: ["eq", "in"] },
  { name: "session_id", ops: ["eq", "in"] },
  { name: "enabled", ops: ["eq"] },
  { name: "schedule", ops: ["eq", "contains"] },
];

function PromptDetail({ id }: { id: string }) {
  const client = useAdminClient();
  const [detail, setDetail] = useState<unknown>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    fetchPeriodicPrompt(client, id, ac.signal)
      .then((res) => setDetail(res.item ?? res))
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)));
    return () => ac.abort();
  }, [client, id]);

  if (err) return <div className="text-sm text-destructive">{err}</div>;
  if (!detail) return <Skeleton className="h-32 w-full" />;
  return <JsonViewer value={detail} title="Periodic prompt detail" />;
}

export function AutomationPage() {
  return (
    <CollectionPage<PeriodicPromptSummary>
      title="Automation"
      description="Periodic prompts, next fire times, and linked runs."
      collection="periodic_prompts"
      columns={columns}
      filterFields={filters}
      rowKey={(r) => r.id}
      detailTitle={(id) => `Periodic prompt ${id.slice(0, 8)}…`}
      renderDetail={(id) => <PromptDetail id={id} />}
    />
  );
}
