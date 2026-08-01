import type { SessionEvent } from "@client/gen/ultra/v1/event_pb";

export type TimelineItem =
  | { type: "user"; seq: bigint; text: string }
  | { type: "assistant"; runId: string; text: string; streaming: boolean }
  | { type: "tool"; runId: string; name: string; input: string; output?: string; error?: boolean }
  | { type: "question"; runId: string; text: string; choices: string[] }
  | { type: "status"; runId: string; status: string; message?: string }
  // Annotations may be attributed to a run so lane filtering can include them.
  | { type: "annotation"; text: string; runId?: string };

export type EnvLifecycleState = {
  envId: string;
  name: string;
  phase: string;
  epoch: number;
  message?: string;
};

export type SessionView = {
  items: TimelineItem[];
  /** spawns records parent/child links seen in the log, so lanes are known
   * even before the run tree is fetched. */
  spawns: { parentRunId: string; childRunId: string }[];
  lastSeq: bigint;
  /**
   * deltaFrames counts the streamed assistant deltas folded so far.
   * Incremental-rendering evidence asserts against it: a client that only
   * paints the final text can never advance it past one.
   */
  deltaFrames: number;
  /** envs tracks the last observed lifecycle phase per environment. */
  envs: Record<string, EnvLifecycleState>;
};

export const initialView: SessionView = { items: [], lastSeq: 0n, deltaFrames: 0, envs: {}, spawns: [] };

const envPhases: Record<string, string> = {
  envRequested: "requested",
  envProvisioning: "provisioning",
  envReady: "ready",
  envFailed: "failed",
  envTerminating: "terminating",
  envTerminated: "terminated",
};

/**
 * ViewAction is either a session event to fold or an explicit reset. Reset
 * exists because switching sessions or replicas must discard everything and
 * rebuild from the log, rather than layering a new stream over stale state.
 */
export type ViewAction = { reset: true } | { event: SessionEvent };

export function reduceView(state: SessionView, action: ViewAction): SessionView {
  if ("reset" in action) return initialView;
  return foldEvent(state, action.event);
}

export function foldEvent(state: SessionView, event: SessionEvent): SessionView {
  if (event.seq <= state.lastSeq) return state;
  const items = [...state.items];
  let deltaFrames = state.deltaFrames;
  const envs = { ...state.envs };
  const spawns = [...state.spawns];
  const payload = event.payload?.payload;
  if (!payload) return { ...state, items, lastSeq: event.seq };


  switch (payload.case) {
    case "userMessage":
      items.push({ type: "user", seq: event.seq, text: payload.value.text });
      break;
    case "annotation":
      items.push({ type: "annotation", text: payload.value.text });
      break;
    case "runStarted":
      items.push({ type: "status", runId: payload.value.runId, status: "running" });
      break;
    case "textDelta": {
      const d = payload.value;
      deltaFrames += 1;
      const existing = [...items].reverse().find((i) => i.type === "assistant" && i.runId === d.runId && i.streaming);
      if (existing?.type === "assistant") existing.text += d.text;
      else items.push({ type: "assistant", runId: d.runId, text: d.text, streaming: true });
      break;
    }
    case "runSpawned":
      spawns.push({ parentRunId: payload.value.parentRunId, childRunId: payload.value.childRunId });
      items.push({
        type: "annotation",
        text: `spawned agent ${payload.value.childRunId.slice(0, 8)}`,
        runId: payload.value.parentRunId,
      });
      break;
    case "memorySet":
      items.push({ type: "annotation", text: `memory set: ${payload.value.key}` });
      break;
    case "memoryDeleted":
      items.push({ type: "annotation", text: `memory deleted: ${payload.value.key}` });
      break;
    case "permissionDenied":
      items.push({
        type: "status",
        runId: payload.value.runId,
        status: "denied",
        message: `${payload.value.tool}: ${payload.value.reason}`,
      });
      break;
    case "toolCallStarted":
      items.push({ type: "tool", runId: payload.value.runId, name: payload.value.name, input: payload.value.input });
      break;
    case "toolResult": {
      const result = payload.value;
      const tool = [...items].reverse().find((i) => i.type === "tool" && i.runId === result.runId && i.name === result.name && i.output === undefined);
      if (tool?.type === "tool") { tool.output = result.content; tool.error = result.isError; }
      break;
    }
    case "runAwaiting": {
      const q = payload.value;
      items.push({ type: "question", runId: q.runId, text: q.question?.text ?? "", choices: q.question?.choices ?? [] });
      break;
    }
    case "runCompleted":
      for (const item of items) if (item.type === "assistant" && item.runId === payload.value.runId) item.streaming = false;
      items.push({ type: "status", runId: payload.value.runId, status: "completed" });
      break;
    case "runFailed":
      items.push({ type: "status", runId: payload.value.runId, status: "failed", message: payload.value.message });
      break;
    case "runCancelled":
      items.push({ type: "status", runId: payload.value.runId, status: "cancelled" });
      break;
    case "execPreviewRan":
      items.push({ type: "tool", runId: "human", name: `exec: ${payload.value.command}`, input: payload.value.command, output: payload.value.output, error: payload.value.isError });
      break;
    case "envRequested": case "envProvisioning": case "envReady": case "envFailed": case "envTerminating": case "envTerminated": {
      const env = payload.value;
      const phase = envPhases[payload.case] ?? payload.case;
      envs[env.envId] = {
        envId: env.envId,
        name: env.name,
        phase,
        epoch: env.epoch,
        message: env.message || undefined,
      };
      items.push({ type: "annotation", text: `${phase}: ${env.name}${env.message ? ` (${env.message})` : ""}` });
      break;
    }
  }
  return { items, lastSeq: event.seq, deltaFrames, envs, spawns };
}
