import { useCallback, useEffect, useMemo, useReducer, useState } from "react";
import type { Org, ProviderInstance } from "@client/gen/ultra/v1/org_pb";
import type { Session } from "@client/gen/ultra/v1/session_pb";
import type { DevEnv, UsageInterval } from "@client/gen/ultra/v1/env_pb";
import type { RunTreeNode } from "@client/gen/ultra/v1/agent_pb";
import type { Flow, FlowFieldError, FlowInvocation } from "@client/gen/ultra/v1/flow_pb";
import { FlowFieldErrorSchema } from "@client/gen/ultra/v1/flow_pb";
import { ConnectError } from "@connectrpc/connect";
import { EnvState } from "@client/gen/ultra/v1/env_pb";
import { clients } from "./api";
import { initialView, reduceView } from "./reducer";
import { EnvironmentPanel } from "@/components/environment-panel";
import { FlowInvocationView, FlowPanel, invocationStateLabel } from "@/components/flow-panel";
import { MemoryPanel } from "@/components/memory-panel";
import { RunTree } from "@/components/run-tree";
import { SessionSidebar, type ConnectionState } from "@/components/session-sidebar";
import { SettingsView, type CredentialForm, type ProviderForm } from "@/components/settings-view";
import { Timeline } from "@/components/timeline";
import { UsagePanel } from "@/components/usage-panel";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const apiBaseUrl = import.meta.env.VITE_ULTRAD_URL ?? "http://localhost:8080";
// A deployment runs several ultrad replicas. The alternate address lets an
// operator reconnect through a different one, which is how a client proves it
// depends on the durable log rather than on one server's memory.
const altBaseUrl = import.meta.env.VITE_ULTRAD_ALT_URL ?? "";
const initialToken = localStorage.getItem("ultra-token") ?? "dev-token";

/**
 * flowFieldErrors pulls the structured validation failures out of a rejected
 * flow request. The server attaches them as typed error details precisely so
 * every client can show the same field paths instead of parsing prose.
 */
function flowFieldErrors(error: unknown): FlowFieldError[] {
  return ConnectError.from(error).findDetails(FlowFieldErrorSchema);
}

export function App() {
  const [token, setToken] = useState(initialToken);
  const [endpoint, setEndpoint] = useState(apiBaseUrl);
  const api = useMemo(() => clients(endpoint, token), [endpoint, token]);
  const [orgs, setOrgs] = useState<Org[]>([]);
  const [org, setOrg] = useState<Org>();
  const [sessions, setSessions] = useState<Session[]>([]);
  const [session, setSession] = useState<Session>();
  const [view, dispatch] = useReducer(reduceView, initialView);
  const [connection, setConnection] = useState<ConnectionState>("connecting");
  const [prompt, setPrompt] = useState("");
  const [title, setTitle] = useState("");
  const [settings, setSettings] = useState(false);
  const [envs, setEnvs] = useState<DevEnv[]>([]);
  const [envOutput, setEnvOutput] = useState("");
  const [usage, setUsage] = useState<UsageInterval[]>([]);
  const [usageTotal, setUsageTotal] = useState(0n);
  const [participants, setParticipants] = useState<string[]>([]);
  const [runTree, setRunTree] = useState<RunTreeNode[]>([]);
  const [laneRunId, setLaneRunId] = useState<string>();
  const [memory, setMemory] = useState<{ key: string; valueJson: string }[]>([]);
  const [flows, setFlows] = useState<Flow[]>([]);
  const [flowVersions, setFlowVersions] = useState<Flow[]>([]);
  const [selectedFlow, setSelectedFlow] = useState<string>();
  const [selectedVersion, setSelectedVersion] = useState(0);
  const [definition, setDefinition] = useState("");
  const [flowErrors, setFlowErrors] = useState<FlowFieldError[]>([]);
  const [invocations, setInvocations] = useState<FlowInvocation[]>([]);
  const [activeInvocationId, setActiveInvocationId] = useState<string>();
  const [credential, setCredential] = useState<CredentialForm>({ apiKey: "", baseUrl: "", extraHeaders: "{}" });
  const [providers, setProviders] = useState<ProviderInstance[]>([]);
  const [provider, setProvider] = useState<ProviderForm>({ kind: "byo_k8s", name: "", config: '{"mode":"loopback"}' });
  const [error, setError] = useState("");

  // A direct invocation is one opened from its identifier alone, which is the
  // path an operator follows from a CLI or an alert. It is kept separate from
  // the session's list so opening one never depends on having loaded the list.
  const [directInvocation, setDirectInvocation] = useState<FlowInvocation>();
  const activeInvocation =
    directInvocation ?? invocations.find((i) => i.id === activeInvocationId) ?? invocations[0];

  // openInvocation fetches one invocation by id. Failures surface as errors
  // rather than an empty panel: an operator following a link needs to know the
  // difference between "not yours" and "still loading".
  const openInvocation = useCallback(
    async (id: string) => {
      try {
        const resp = await api.flows.getFlowInvocation({ invocationId: id });
        if (resp.invocation) {
          setDirectInvocation(resp.invocation);
          setActiveInvocationId(resp.invocation.id);
        }
        setError("");
      } catch (e) {
        setDirectInvocation(undefined);
        setError(String(e));
      }
    },
    [api],
  );

  // An invocation named in the address is opened directly, and kept fresh
  // while it is still running.
  const requestedInvocation = new URLSearchParams(window.location.search).get("invocation") ?? "";
  useEffect(() => {
    if (!requestedInvocation) return;
    void openInvocation(requestedInvocation);
    const terminal = ["completed", "failed", "cancelled"];
    const timer = window.setInterval(() => {
      if (directInvocation && terminal.includes(invocationStateLabel(directInvocation.state))) return;
      void openInvocation(requestedInvocation);
    }, 500);
    return () => window.clearInterval(timer);
  }, [requestedInvocation, openInvocation, directInvocation]);

  const load = useCallback(async () => {
    try {
      const listed = await api.orgs.listOrgs({});
      setOrgs(listed.orgs);
      const selected = org ?? listed.orgs[0];
      setOrg(selected);
      if (selected) {
        setSessions((await api.sessions.listSessions({ orgId: selected.id })).sessions);
        setProviders((await api.orgs.listProviders({ orgId: selected.id })).providers);
      }
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }, [api, org]);
  useEffect(() => {
    void load();
  }, [load]);

  const refreshEnvs = useCallback(async () => {
    if (!session) return;
    setEnvs((await api.envs.listEnvs({ sessionId: session.id })).envs);
  }, [api, session]);

  const refreshUsage = useCallback(async () => {
    if (!org) return;
    const resp = await api.billing.getUsage({ orgId: org.id });
    setUsage(resp.intervals);
    setUsageTotal(resp.totalSeconds);
  }, [api, org]);

  const refreshRunTree = useCallback(async () => {
    if (!session) return;
    const resp = await api.agents.getRunTree({ sessionId: session.id });
    setRunTree(resp.roots);
  }, [api, session]);

  const refreshMultiplayer = useCallback(async () => {
    if (!session) return;
    const p = await api.sessions.listParticipants({ sessionId: session.id });
    setParticipants(p.participants.filter((x) => x.state === "active").map((x) => x.display || x.participantId));
    const m = await api.sessions.listMemory({ sessionId: session.id });
    setMemory(m.entries);
  }, [api, session]);

  const refreshFlows = useCallback(async () => {
    if (!org) return;
    setFlows((await api.flows.listFlows({ orgId: org.id })).flows);
  }, [api, org]);

  const refreshFlowVersions = useCallback(
    async (name: string) => {
      if (!org || !name) return;
      const resp = await api.flows.listFlowVersions({ orgId: org.id, name });
      setFlowVersions(resp.flows);
      const latest = resp.flows[0];
      if (latest) {
        setSelectedVersion(latest.version);
        setDefinition(latest.definitionJson);
      }
    },
    [api, org],
  );

  // Invocations are refreshed from the API rather than reconstructed from the
  // log: the invocation view carries progress, runs, and environments together,
  // and assembling it client-side would race the stream.
  const refreshInvocations = useCallback(async () => {
    if (!session) return;
    const resp = await api.flows.listFlowInvocations({ sessionId: session.id });
    setInvocations(resp.invocations);
  }, [api, session]);

  useEffect(() => {
    if (!session) return;
    void api.sessions.join({ sessionId: session.id, display: "You" }).then(refreshMultiplayer);
    void refreshEnvs();
    void refreshUsage();
    void refreshRunTree();
    void refreshFlows();
    void refreshInvocations();
  }, [session, api, refreshMultiplayer, refreshEnvs, refreshUsage, refreshRunTree, refreshFlows, refreshInvocations]);

  useEffect(() => {
    if (!session) return;
    const abort = new AbortController();
    setConnection("connecting");
    (async () => {
      try {
        for await (const resp of api.events.subscribe({ sessionId: session.id, fromSeq: 0n }, { signal: abort.signal })) {
          setConnection("live");
          if (resp.event) dispatch({ event: resp.event });
        }
        if (!abort.signal.aborted) setConnection("offline");
      } catch (e) {
        if (!abort.signal.aborted) {
          setConnection("offline");
          setError(String(e));
        }
      }
    })();
    return () => abort.abort();
  }, [api, session]);

  // Environment lifecycle arrives as events; keep the panel and the ledger in
  // step with them rather than polling on a timer.
  const envSignature = Object.values(view.envs)
    .map((e) => `${e.envId}:${e.phase}:${e.epoch}`)
    .join(",");
  useEffect(() => {
    if (!envSignature) return;
    void refreshEnvs();
    void refreshUsage();
  }, [envSignature, refreshEnvs, refreshUsage]);

  // Memory changes are announced in the log, so the inspector refreshes on
  // those events rather than only when the session is first opened.
  const memorySignature = view.items
    .filter((i) => i.type === "annotation" && i.text.startsWith("memory "))
    .map((i) => (i.type === "annotation" ? i.text : ""))
    .join(",");
  useEffect(() => {
    if (!session) return;
    void refreshMultiplayer();
  }, [memorySignature, session, refreshMultiplayer]);

  // Flow lifecycle is announced in the log, so the invocation view refreshes
  // on those events rather than on a timer.
  const flowSignature = view.flows.map((f) => `${f.invocationId}:${f.state}:${f.progress.length}`).join(",");
  useEffect(() => {
    if (!session) return;
    void refreshInvocations();
  }, [flowSignature, session, refreshInvocations]);

  // Run structure changes are announced in the log (spawns and run status), so
  // the tree refreshes on those rather than on a timer.
  const runSignature = [
    view.spawns.map((s) => s.childRunId).join(","),
    view.items.filter((i) => i.type === "status").map((i) => `${i.runId}:${i.status}`).join(","),
  ].join("|");
  useEffect(() => {
    if (!session) return;
    void refreshRunTree();
  }, [runSignature, session, refreshRunTree]);

  async function createSession() {
    if (!org) return;
    const created = await api.sessions.createSession({ orgId: org.id, title: title || "New session" });
    if (created.session) {
      setSessions((s) => [created.session!, ...s]);
      setSession(created.session);
    }
    setTitle("");
  }
  async function sendPrompt() {
    if (!session || !prompt.trim()) return;
    await api.events.append({ sessionId: session.id, payload: { payload: { case: "userMessage", value: { text: prompt } } } });
    await api.agents.startRun({ sessionId: session.id, prompt });
    setPrompt("");
  }
  async function answer(runId: string, message: string) {
    await api.agents.promptRun({ runId, message });
  }
  async function provisionEnv() {
    if (!session) return;
    await api.envs.provisionEnv({
      sessionId: session.id,
      spec: { name: "main", workdir: "/work", env: {}, metadata: {} },
      providerInstance: "default",
    });
    await refreshEnvs();
  }
  async function restartEnv(envId: string) {
    try {
      await api.envs.restartEnv({ envId });
      await refreshEnvs();
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }
  async function terminateEnv(envId: string) {
    try {
      await api.envs.terminateEnv({ envId });
      await refreshEnvs();
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }
  async function execPreview(command: string) {
    const env = envs.find((e) => e.state === EnvState.READY);
    if (!env || !command) return;
    try {
      const r = await api.envs.execPreview({ envId: env.id, command });
      setEnvOutput(r.output);
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }
  async function validateFlow() {
    if (!org) return;
    try {
      const resp = await api.flows.validateFlow({ orgId: org.id, definitionJson: definition });
      setFlowErrors(resp.errors);
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }
  async function saveFlow(name: string) {
    if (!org || !name) return;
    try {
      await api.flows.putFlow({ orgId: org.id, name, definitionJson: definition });
      setFlowErrors([]);
      await refreshFlows();
      await selectFlow(name);
      setError("");
    } catch (e) {
      // A rejected save carries its field paths as typed error details, so the
      // browser renders the same structured list the CLI prints rather than a
      // reworded message.
      const fields = flowFieldErrors(e);
      if (fields.length > 0) {
        setFlowErrors(fields);
        return;
      }
      setError(String(e));
    }
  }
  async function selectFlow(name: string) {
    setSelectedFlow(name);
    setFlowErrors([]);
    await refreshFlowVersions(name);
  }
  function selectVersion(version: number) {
    setSelectedVersion(version);
    const found = flowVersions.find((f) => f.version === version);
    if (found) setDefinition(found.definitionJson);
  }
  async function invokeFlow(params: Record<string, unknown>) {
    if (!session || !selectedFlow) return;
    try {
      const resp = await api.flows.invokeFlow({
        sessionId: session.id,
        name: selectedFlow,
        version: selectedVersion,
        paramsJson: JSON.stringify(params),
      });
      setActiveInvocationId(resp.invocationId);
      setFlowErrors([]);
      await refreshInvocations();
      setError("");
    } catch (e) {
      const fields = flowFieldErrors(e);
      if (fields.length > 0) {
        setFlowErrors(fields);
        return;
      }
      setError(String(e));
    }
  }
  async function cancelInvocation(id: string) {
    try {
      await api.flows.cancelFlowInvocation({ invocationId: id });
      await refreshInvocations();
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }
  async function rememberMemory(key: string, valueJson: string) {
    if (!session || !key) return;
    try {
      await api.sessions.setMemory({ sessionId: session.id, key, valueJson: valueJson || '""' });
      await refreshMultiplayer();
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }
  async function saveCredential() {
    if (!org || !credential.apiKey) return;
    try {
      const parsed = JSON.parse(credential.extraHeaders);
      if (parsed === null || Array.isArray(parsed) || typeof parsed !== "object" || Object.values(parsed).some((v) => typeof v !== "string")) {
        throw new Error("Headers must be a JSON object of string values");
      }
      await api.orgs.putCredential({
        orgId: org.id,
        kind: "inference:openai",
        name: "default",
        apiKey: credential.apiKey,
        baseUrl: credential.baseUrl,
        extraHeadersJson: JSON.stringify(parsed),
      });
      setCredential({ apiKey: "", baseUrl: "", extraHeaders: "{}" });
      setSettings(false);
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }
  async function registerProvider() {
    if (!org) return;
    try {
      JSON.parse(provider.config);
      await api.orgs.registerProvider({ orgId: org.id, kind: provider.kind, name: provider.name, configJson: provider.config });
      setProviders((await api.orgs.listProviders({ orgId: org.id })).providers);
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }
  function changeToken(value: string) {
    localStorage.setItem("ultra-token", value);
    setToken(value);
  }
  // Switching replicas tears down the subscription and rebuilds every view
  // from the log on the other server, so nothing carries over in memory.
  function switchEndpoint() {
    if (!altBaseUrl) return;
    setEndpoint((current: string) => (current === apiBaseUrl ? altBaseUrl : apiBaseUrl));
    setConnection("connecting");
    dispatch({ reset: true });
  }

  return (
    <div className="flex min-h-screen bg-zinc-950 text-zinc-100">
      <SessionSidebar
        orgs={orgs}
        org={org}
        onSelectOrg={(id) => setOrg(orgs.find((o) => o.id === id))}
        sessions={sessions}
        session={session}
        onSelectSession={setSession}
        title={title}
        onTitleChange={setTitle}
        onCreateSession={createSession}
        connection={connection}
        token={token}
        onTokenChange={changeToken}
        onToggleSettings={() => setSettings(!settings)}
        endpoint={endpoint}
        altEndpoint={altBaseUrl}
        onSwitchEndpoint={switchEndpoint}
      />
      <main className="mx-auto flex h-screen max-w-4xl flex-1 flex-col gap-4 p-6">
        {settings ? (
          <SettingsView
            credential={credential}
            onCredentialChange={setCredential}
            onSaveCredential={saveCredential}
            provider={provider}
            onProviderChange={setProvider}
            onRegisterProvider={registerProvider}
            providers={providers}
          />
        ) : directInvocation ? (
          <FlowInvocationView
            invocation={directInvocation}
            onCancel={cancelInvocation}
            onClose={() => {
              setDirectInvocation(undefined);
              window.history.replaceState(null, "", window.location.pathname);
            }}
          />
        ) : session ? (
          <>
            <header className="flex items-start justify-between border-b border-zinc-800 pb-4">
              <div>
                <h2 className="text-xl font-medium">{session.title}</h2>
                <div data-testid="presence" className="text-xs text-zinc-500">
                  {participants.join(", ")}
                </div>
              </div>
            </header>
            <FlowPanel
              flows={flows}
              versions={flowVersions}
              selectedFlow={selectedFlow}
              onSelectFlow={selectFlow}
              selectedVersion={selectedVersion}
              onSelectVersion={selectVersion}
              definition={definition}
              onDefinitionChange={setDefinition}
              validationErrors={flowErrors}
              onValidate={validateFlow}
              onSave={saveFlow}
              onInvoke={invokeFlow}
              invocations={invocations}
              activeInvocation={activeInvocation}
              onSelectInvocation={setActiveInvocationId}
              onCancelInvocation={cancelInvocation}
            />
            <RunTree roots={runTree} selectedRunId={laneRunId} onSelectRun={setLaneRunId} />
            <EnvironmentPanel
              envs={envs}
              output={envOutput}
              onProvision={provisionEnv}
              onRestart={restartEnv}
              onTerminate={terminateEnv}
              onExec={execPreview}
            />
            <UsagePanel intervals={usage} totalSeconds={usageTotal} onRefresh={refreshUsage} />
            <MemoryPanel entries={memory} onSet={rememberMemory} />
            <Timeline items={view.items} onAnswer={answer} deltaFrames={view.deltaFrames} laneRunId={laneRunId} />
            <div className="flex gap-2 border-t border-zinc-800 pt-4">
              <Input
                aria-label="Prompt"
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") void sendPrompt();
                }}
                placeholder="Ask an agent…"
              />
              <Button onClick={sendPrompt}>Send</Button>
            </div>
          </>
        ) : (
          <div className="m-auto text-zinc-500">Create or select a session</div>
        )}
        {error && <Alert className="fixed bottom-4 right-4 max-w-md">{error}</Alert>}
      </main>
    </div>
  );
}
