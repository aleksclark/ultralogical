import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { listFilterHref } from "@/components/EntityLink";
import { useCollection } from "@/data/useCollection";
import { defaultQueryState } from "@/query/state";
import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Input,
  Label,
  PageHeader,
} from "@/components/ui";
import { formatTs } from "@/lib/format";
import type { EventSummary, RunSummary } from "@admin-gen/admin/v1/admin_pb";

/**
 * Security diagnostics from available read data (actor search across events/runs).
 * Full operator audit log arrives in E7.
 */
export function SecurityPage() {
  const [actor, setActor] = useState("");
  const [submitted, setSubmitted] = useState("");

  const eventsState = defaultQueryState({
    filters: submitted
      ? [
          { field: "actor_id", op: "eq", value: submitted },
        ]
      : [],
    limit: 25,
  });
  const runsState = defaultQueryState({
    filters: submitted
      ? [
          { field: "actor_id", op: "eq", value: submitted },
        ]
      : [],
    limit: 25,
  });

  const events = useCollection<EventSummary>("events", eventsState);
  const runs = useCollection<RunSummary>("runs", runsState);

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitted(actor.trim());
  }

  return (
    <div data-testid="security-page">
      <PageHeader
        title="Security diagnostics"
        description="Actor attribution search across events and runs. Operator audit log is E7."
      />

      <Card className="mb-4">
        <CardContent className="py-3">
          <form className="flex flex-wrap items-end gap-3" onSubmit={onSubmit}>
            <div className="min-w-[16rem] flex-1 space-y-1">
              <Label htmlFor="actor">Actor ID</Label>
              <Input
                id="actor"
                data-testid="security-actor-input"
                value={actor}
                onChange={(e) => setActor(e.target.value)}
                placeholder="run id, service name, user id…"
              />
            </div>
            <Button type="submit" data-testid="security-actor-search">
              Search
            </Button>
          </form>
          <div className="mt-3 flex flex-wrap gap-3 text-xs">
            <Link className="text-primary hover:underline" to={listFilterHref("/api-keys", [])}>
              API key metadata
            </Link>
            <Link className="text-primary hover:underline" to={listFilterHref("/credentials", [])}>
              Credential metadata
            </Link>
            <Link
              className="text-primary hover:underline"
              to={listFilterHref("/runs", [{ field: "state", value: "failed" }])}
            >
              Failed runs
            </Link>
          </div>
        </CardContent>
      </Card>

      {!submitted ? (
        <p className="text-sm text-muted-foreground">Enter an actor id to search events and runs.</p>
      ) : (
        <div className="grid gap-3 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>Events · actor_id={submitted}</CardTitle>
            </CardHeader>
            <CardContent>
              {events.status === "loading" ? (
                <div className="text-sm text-muted-foreground">Loading…</div>
              ) : events.status === "error" ? (
                <div className="text-sm text-destructive">{events.error}</div>
              ) : (events.data?.items.length ?? 0) === 0 ? (
                <div className="text-sm text-muted-foreground">No events.</div>
              ) : (
                <ul className="space-y-2 text-xs" data-testid="security-events">
                  {events.data!.items.map((ev) => (
                    <li key={`${ev.sessionId}:${ev.seq}`} className="rounded border px-2 py-1.5">
                      <div className="flex justify-between gap-2">
                        <span className="font-medium">{ev.kind}</span>
                        <span className="text-muted-foreground">{formatTs(ev.ts)}</span>
                      </div>
                      <div className="text-muted-foreground">
                        session {ev.sessionId.slice(0, 8)}… · seq {String(ev.seq)}
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Runs · actor_id={submitted}</CardTitle>
            </CardHeader>
            <CardContent>
              {runs.status === "loading" ? (
                <div className="text-sm text-muted-foreground">Loading…</div>
              ) : runs.status === "error" ? (
                <div className="text-sm text-destructive">{runs.error}</div>
              ) : (runs.data?.items.length ?? 0) === 0 ? (
                <div className="text-sm text-muted-foreground">No runs.</div>
              ) : (
                <ul className="space-y-2 text-xs" data-testid="security-runs">
                  {runs.data!.items.map((r) => (
                    <li key={r.id} className="rounded border px-2 py-1.5">
                      <Link className="font-medium text-primary hover:underline" to={`/runs/${r.id}`}>
                        {r.id.slice(0, 8)}… · {r.state}
                      </Link>
                      <div className="text-muted-foreground">{formatTs(r.createdAt)}</div>
                    </li>
                  ))}
                </ul>
              )}
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  );
}
