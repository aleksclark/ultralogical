import type { UsageInterval } from "@client/gen/ultra/v1/env_pb";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export function UsagePanel({
  intervals,
  totalSeconds,
  onRefresh,
}: {
  intervals: UsageInterval[];
  totalSeconds: bigint;
  onRefresh: () => Promise<void>;
}) {
  return (
    <Card data-testid="usage-panel">
      <CardHeader className="flex-row items-center justify-between">
        <div>
          <CardTitle>Environment usage</CardTitle>
          <CardDescription>Metered from ready to terminal, bounded by persisted heartbeats.</CardDescription>
        </div>
        <Button size="sm" variant="outline" onClick={onRefresh}>
          Refresh usage
        </Button>
      </CardHeader>
      <CardContent className="space-y-2">
        <p data-testid="usage-total" className="text-sm text-zinc-200">
          Total metered seconds: <span className="font-mono">{totalSeconds.toString()}</span>
        </p>
        <ul className="space-y-1">
          {intervals.map((interval) => (
            <li
              key={`${interval.envId}-${interval.startedAt?.seconds ?? 0}`}
              data-testid="usage-interval"
              data-env-id={interval.envId}
              data-open={interval.open}
              className="flex items-center gap-2 text-xs text-zinc-400"
            >
              <Badge variant={interval.open ? "pending" : "success"}>{interval.open ? "open" : "closed"}</Badge>
              <span className="font-mono">{interval.envId.slice(0, 8)}</span>
              <span>{interval.rateClass}</span>
              <span className="font-mono">{interval.seconds.toString()}s</span>
            </li>
          ))}
          {intervals.length === 0 && <li className="text-xs text-zinc-500">No metered intervals yet</li>}
        </ul>
      </CardContent>
    </Card>
  );
}
