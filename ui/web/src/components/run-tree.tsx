import type { RunTreeNode, RunWait } from "@client/gen/ultra/v1/agent_pb";
import { RunState } from "@client/gen/ultra/v1/agent_pb";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

const runStateLabels: Record<number, string> = {
  [RunState.PENDING]: "pending",
  [RunState.RUNNING]: "running",
  [RunState.AWAITING]: "awaiting",
  [RunState.COMPLETED]: "completed",
  [RunState.FAILED]: "failed",
  [RunState.CANCELLED]: "cancelled",
};

export function runStateLabel(state: RunState): string {
  return runStateLabels[state] ?? "unknown";
}

function stateVariant(state: RunState) {
  if (state === RunState.COMPLETED) return "success" as const;
  if (state === RunState.FAILED || state === RunState.CANCELLED) return "destructive" as const;
  return "pending" as const;
}

/**
 * RunTree renders a session's spawn tree. A parent and its children are shown
 * as one nested structure, because "which agent did this" is otherwise
 * unanswerable in a session where several agents work at once.
 *
 * Selecting a run filters the timeline to that run's lane.
 */
export function RunTree({
  roots,
  selectedRunId,
  onSelectRun,
}: {
  roots: RunTreeNode[];
  selectedRunId?: string;
  onSelectRun: (runId?: string) => void;
}) {
  const total = countRuns(roots);
  return (
    <Card data-testid="run-tree">
      <CardHeader className="flex-row items-center justify-between">
        <div>
          <CardTitle>Agent runs</CardTitle>
          <CardDescription>
            {total} run{total === 1 ? "" : "s"} in this session
          </CardDescription>
        </div>
        <Button
          size="sm"
          variant={selectedRunId ? "outline" : "default"}
          onClick={() => onSelectRun(undefined)}
          aria-label="Show all runs"
        >
          All lanes
        </Button>
      </CardHeader>
      <CardContent className="space-y-1">
        {roots.map((node) => (
          <RunTreeRow key={node.run?.id} node={node} depth={0} selectedRunId={selectedRunId} onSelectRun={onSelectRun} />
        ))}
        {roots.length === 0 && <p className="text-xs text-zinc-500">No runs yet</p>}
      </CardContent>
    </Card>
  );
}

function countRuns(nodes: RunTreeNode[]): number {
  return nodes.reduce((sum, node) => sum + 1 + countRuns(node.children), 0);
}

function RunTreeRow({
  node,
  depth,
  selectedRunId,
  onSelectRun,
}: {
  node: RunTreeNode;
  depth: number;
  selectedRunId?: string;
  onSelectRun: (runId?: string) => void;
}) {
  const run = node.run;
  if (!run) return null;
  const selected = selectedRunId === run.id;
  return (
    <div data-testid="run-tree-node" data-run-id={run.id} data-depth={depth} data-parent-run-id={run.parentRunId}>
      <button
        onClick={() => onSelectRun(run.id)}
        aria-label={`Show lane for ${run.prompt || run.id}`}
        className={cn(
          "flex w-full items-center gap-2 rounded px-2 py-1 text-left text-xs hover:bg-zinc-900",
          selected && "bg-zinc-800",
        )}
        style={{ paddingLeft: `${depth * 16 + 8}px` }}
      >
        <Badge variant={stateVariant(run.state)} data-run-state={runStateLabel(run.state)}>
          {runStateLabel(run.state)}
        </Badge>
        <span className="truncate">{run.prompt || run.id.slice(0, 8)}</span>
        {run.cohortId && (
          <span className="text-zinc-500" data-testid="cohort-marker" data-cohort-ordinal={run.cohortOrdinal}>
            cohort #{run.cohortOrdinal}
          </span>
        )}
      </button>
      {node.waits.map((wait) => (
        <WaitRow key={wait.id} wait={wait} depth={depth} />
      ))}
      {node.children.map((child) => (
        <RunTreeRow
          key={child.run?.id}
          node={child}
          depth={depth + 1}
          selectedRunId={selectedRunId}
          onSelectRun={onSelectRun}
        />
      ))}
    </div>
  );
}

/**
 * WaitRow shows why a run parked and how that wait ended. A parent shown merely
 * as "awaiting" tells an operator nothing about whether it is progressing,
 * timed out, or was abandoned.
 */
function WaitRow({ wait, depth }: { wait: RunWait; depth: number }) {
  return (
    <div
      data-testid="run-wait"
      data-wait-state={wait.state}
      data-wait-kind={wait.kind}
      data-member-count={wait.memberRunIds.length}
      className="flex items-center gap-2 py-0.5 text-xs text-zinc-400"
      style={{ paddingLeft: `${depth * 16 + 24}px` }}
    >
      <Badge variant={waitVariant(wait.state)}>{wait.state}</Badge>
      <span>
        {wait.kind === "cohort" ? "cohort" : "wait"} on {wait.memberRunIds.length} agent
        {wait.memberRunIds.length === 1 ? "" : "s"}
      </span>
    </div>
  );
}

function waitVariant(state: string) {
  if (state === "resolved") return "success" as const;
  if (state === "timed_out" || state === "abandoned") return "destructive" as const;
  return "pending" as const;
}
