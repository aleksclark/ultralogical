import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import type { TimelineItem } from "@/reducer";

type AnswerFn = (runId: string, message: string) => Promise<void>;

export function Timeline({
  items,
  onAnswer,
  deltaFrames,
  laneRunId,
}: {
  items: TimelineItem[];
  onAnswer: AnswerFn;
  deltaFrames: number;
  /** When set, only this run's activity is shown. Sessions run several agents
   * at once, so an unfiltered timeline interleaves them; a lane answers "what
   * did this one agent do". */
  laneRunId?: string;
}) {
  const visible = laneRunId ? items.filter((item) => runIdOf(item) === laneRunId) : items;
  return (
    <section
      data-testid="timeline"
      data-delta-frames={deltaFrames}
      data-lane={laneRunId ?? ""}
      data-visible-rows={visible.length}
      className="flex-1 space-y-3 overflow-auto py-4"
    >
      {visible.map((item, index) => (
        <TimelineRow key={index} item={item} onAnswer={onAnswer} />
      ))}
      {laneRunId && visible.length === 0 && (
        <p className="text-xs text-zinc-500">No activity for this agent yet</p>
      )}
    </section>
  );
}

/** runIdOf reports which run an item belongs to, if any. */
function runIdOf(item: TimelineItem): string | undefined {
  switch (item.type) {
    case "assistant":
    case "tool":
    case "question":
    case "status":
      return item.runId;
    case "annotation":
      return item.runId;
    default:
      return undefined;
  }
}

function TimelineRow({ item, onAnswer }: { item: TimelineItem; onAnswer: AnswerFn }) {
  switch (item.type) {
    case "user":
      return (
        <div data-kind="user" className="ml-auto max-w-xl rounded-xl bg-zinc-800 p-3 text-sm">
          {item.text}
        </div>
      );
    case "assistant":
      return (
        <div data-kind="assistant" data-streaming={item.streaming} className="max-w-2xl whitespace-pre-wrap text-sm">
          {item.text}
          {item.streaming && <span className="animate-pulse">▍</span>}
        </div>
      );
    case "tool":
      return (
        <Card data-kind="tool" className="bg-zinc-900">
          <details>
            <summary className="cursor-pointer p-3 font-mono text-xs text-zinc-300">{item.name}</summary>
            <CardContent className="pt-0">
              <pre className="overflow-auto text-xs text-zinc-400">
                {item.input}
                {item.output && `\n→ ${item.output}`}
              </pre>
            </CardContent>
          </details>
        </Card>
      );
    case "question":
      return (
        <Card data-kind="question" className="border-amber-800 bg-amber-950/30">
          <CardContent className="space-y-3 p-4">
            <p className="text-sm">{item.text}</p>
            <div className="flex flex-wrap items-center gap-2">
              {item.choices.map((choice) => (
                <Button key={choice} variant="warning" size="sm" onClick={() => onAnswer(item.runId, choice)}>
                  {choice}
                </Button>
              ))}
              <AnswerForm onAnswer={(value) => onAnswer(item.runId, value)} />
            </div>
          </CardContent>
        </Card>
      );
    case "status":
      return (
        <div className="flex items-center gap-2" data-run-id={item.runId}>
          <Badge data-status={item.status} variant={statusVariant(item.status)}>
            {item.status}
          </Badge>
          {item.message && <span className="text-xs text-zinc-500">{item.message}</span>}
        </div>
      );
    case "annotation":
      return (
        <div data-kind="annotation" data-run-id={item.runId} className="text-sm italic text-zinc-400">
          Note: {item.text}
        </div>
      );
  }
}

function statusVariant(status: string) {
  if (status === "completed") return "success" as const;
  if (status === "failed" || status === "cancelled" || status === "denied") return "destructive" as const;
  return "pending" as const;
}

function AnswerForm({ onAnswer }: { onAnswer: (value: string) => Promise<void> }) {
  const [value, setValue] = useState("");
  return (
    <>
      <Input aria-label="Answer" value={value} onChange={(e) => setValue(e.target.value)} className="w-40" />
      <Button variant="warning" size="sm" onClick={() => onAnswer(value)}>
        Answer
      </Button>
    </>
  );
}
