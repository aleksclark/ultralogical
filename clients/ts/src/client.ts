import { createClient as createConnectClient, type Client as ConnectClient, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-node";
import { TenantService } from "./gen/core/v1/tenant_pb.js";
import { CredentialService } from "./gen/core/v1/credential_pb.js";
import { ProviderService } from "./gen/core/v1/provider_pb.js";
import { SessionService } from "./gen/core/v1/session_pb.js";
import { RunService, RunState, type AgentRun, type ModelConfig, type RunPolicy } from "./gen/core/v1/run_pb.js";
import { ResourceService } from "./gen/core/v1/resource_pb.js";
import { EventService, type SessionEvent } from "./gen/core/v1/event_pb.js";
import { AutomationService } from "./gen/core/v1/automation_pb.js";
import type { LabelSelector } from "./gen/core/v1/session_pb.js";

export type ClientOptions = {
  baseUrl: string;
  apiKey: string;
  actor?: string;
  httpVersion?: "1.1" | "2";
};

export type SubscribeOptions = {
  fromSeq?: bigint;
  reconnect?: boolean;
};

export type AwaitRunOptions = {
  timeoutMs?: number;
  states?: RunState[];
  pollIntervalMs?: number;
};

export type Client = {
  tenants: ConnectClient<typeof TenantService>;
  credentials: ConnectClient<typeof CredentialService>;
  providers: ConnectClient<typeof ProviderService>;
  sessions: ConnectClient<typeof SessionService>;
  runs: ConnectClient<typeof RunService>;
  resources: ConnectClient<typeof ResourceService>;
  events: ConnectClient<typeof EventService>;
  automation: ConnectClient<typeof AutomationService>;
  appendUserMessage(sessionId: string, text: string): Promise<bigint>;
  subscribe(sessionId: string, opts?: SubscribeOptions): AsyncGenerator<SessionEvent, void, unknown>;
  startRun(
    sessionId: string,
    prompt: string,
    model?: ModelConfig,
    policy?: RunPolicy,
  ): Promise<{ run: AgentRun; eventSeq: bigint }>;
  answerRun(runId: string, message: string): Promise<bigint>;
  awaitRun(runId: string, opts?: AwaitRunOptions): Promise<AgentRun>;
  startAndAwait(
    sessionId: string,
    prompt: string,
    model?: ModelConfig,
    policy?: RunPolicy,
    opts?: AwaitRunOptions,
  ): Promise<AgentRun>;
  getEvents(sessionId: string, fromSeq?: bigint, toSeq?: bigint): Promise<SessionEvent[]>;
};

export function labelEq(key: string, value: string): LabelSelector {
  return { key, op: "=", values: [value] } as LabelSelector;
}

export function labelIn(key: string, ...values: string[]): LabelSelector {
  return { key, op: "in", values } as LabelSelector;
}

export function eventKind(ev: SessionEvent): string {
  const p = ev.payload?.payload;
  if (!p || !("case" in p) || !p.case) return "unknown";
  return String(p.case)
    .replace(/[A-Z]/g, (m) => "_" + m.toLowerCase())
    .replace(/^_/, "");
}

function authInterceptor(apiKey: string, actor?: string): Interceptor {
  return (next) => async (req) => {
    req.header.set("Authorization", `Bearer ${apiKey}`);
    if (actor) req.header.set("X-Core-Actor", actor);
    return next(req);
  };
}

export function createClient(opts: ClientOptions): Client {
  const transport = createConnectTransport({
    baseUrl: opts.baseUrl,
    httpVersion: opts.httpVersion ?? "1.1",
    interceptors: [authInterceptor(opts.apiKey, opts.actor)],
  });

  const tenants = createConnectClient(TenantService, transport);
  const credentials = createConnectClient(CredentialService, transport);
  const providers = createConnectClient(ProviderService, transport);
  const sessions = createConnectClient(SessionService, transport);
  const runs = createConnectClient(RunService, transport);
  const resources = createConnectClient(ResourceService, transport);
  const events = createConnectClient(EventService, transport);
  const automation = createConnectClient(AutomationService, transport);

  async function appendUserMessage(sessionId: string, text: string): Promise<bigint> {
    const resp = await events.append({
      sessionId,
      payload: {
        payload: { case: "userMessage", value: { text } },
      },
    });
    return resp.seq;
  }

  async function* subscribe(
    sessionId: string,
    subOpts: SubscribeOptions = {},
  ): AsyncGenerator<SessionEvent, void, unknown> {
    let lastSeq = subOpts.fromSeq ?? 0n;
    const reconnect = subOpts.reconnect !== false;
    for (;;) {
      try {
        const stream = events.subscribe({ sessionId, fromSeq: lastSeq });
        for await (const msg of stream) {
          if (msg.event) {
            lastSeq = msg.event.seq;
            yield msg.event;
          }
        }
        if (!reconnect) return;
      } catch (err) {
        if (!reconnect) throw err;
        await new Promise((r) => setTimeout(r, 50));
      }
    }
  }

  async function startRun(
    sessionId: string,
    prompt: string,
    model?: ModelConfig,
    policy?: RunPolicy,
  ) {
    const resp = await runs.startRun({
      sessionId,
      prompt,
      modelConfig: model,
      policy,
    });
    if (!resp.run) throw new Error("startRun returned no run");
    return { run: resp.run, eventSeq: resp.eventSeq };
  }

  async function answerRun(runId: string, message: string): Promise<bigint> {
    const resp = await runs.answerRun({ runId, message });
    return resp.eventSeq;
  }

  async function awaitRun(runId: string, awaitOpts: AwaitRunOptions = {}): Promise<AgentRun> {
    const timeoutMs = awaitOpts.timeoutMs ?? 120_000;
    const poll = awaitOpts.pollIntervalMs ?? 50;
    const states = new Set(
      awaitOpts.states ?? [
        RunState.COMPLETED,
        RunState.FAILED,
        RunState.CANCELLED,
        RunState.AWAITING,
      ],
    );
    const deadline = Date.now() + timeoutMs;
    let last: AgentRun | undefined;
    while (Date.now() < deadline) {
      const resp = await runs.getRun({ runId });
      last = resp.run;
      if (last && states.has(last.state)) return last;
      await new Promise((r) => setTimeout(r, poll));
    }
    throw new Error(`run ${runId}: timed out (last ${last?.state})`);
  }

  async function startAndAwait(
    sessionId: string,
    prompt: string,
    model?: ModelConfig,
    policy?: RunPolicy,
    awaitOpts?: AwaitRunOptions,
  ) {
    const { run } = await startRun(sessionId, prompt, model, policy);
    return awaitRun(run.id, awaitOpts);
  }

  async function getEvents(sessionId: string, fromSeq = 0n, toSeq = 0n): Promise<SessionEvent[]> {
    const out: SessionEvent[] = [];
    let pageToken = "";
    for (;;) {
      const resp = await events.get({
        sessionId,
        fromSeq,
        toSeq,
        pageSize: 256,
        pageToken,
      });
      out.push(...resp.events);
      if (!resp.nextPageToken) return out;
      pageToken = resp.nextPageToken;
    }
  }

  return {
    tenants,
    credentials,
    providers,
    sessions,
    runs,
    resources,
    events,
    automation,
    appendUserMessage,
    subscribe,
    startRun,
    answerRun,
    awaitRun,
    startAndAwait,
    getEvents,
  };
}
