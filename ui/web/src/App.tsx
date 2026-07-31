import { useCallback, useEffect, useMemo, useReducer, useState } from "react";
import type { Org } from "@client/gen/ultra/v1/org_pb";
import type { Session } from "@client/gen/ultra/v1/session_pb";
import type { DevEnv, UsageInterval } from "@client/gen/ultra/v1/env_pb";
import { EnvState } from "@client/gen/ultra/v1/env_pb";
import { clients } from "./api";
import { foldEvent, initialView } from "./reducer";
import { EnvironmentPanel } from "@/components/environment-panel";
import { SessionSidebar, type ConnectionState } from "@/components/session-sidebar";
import { SettingsView, type CredentialForm, type ProviderForm } from "@/components/settings-view";
import { Timeline } from "@/components/timeline";
import { UsagePanel } from "@/components/usage-panel";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const apiBaseUrl = import.meta.env.VITE_ULTRAD_URL ?? "http://localhost:8080";
const initialToken = localStorage.getItem("ultra-token") ?? "dev-token";

export function App() {
  const [token, setToken] = useState(initialToken);
  const api = useMemo(() => clients(apiBaseUrl, token), [token]);
  const [orgs, setOrgs] = useState<Org[]>([]);
  const [org, setOrg] = useState<Org>();
  const [sessions, setSessions] = useState<Session[]>([]);
  const [session, setSession] = useState<Session>();
  const [view, dispatch] = useReducer(foldEvent, initialView);
  const [connection, setConnection] = useState<ConnectionState>("connecting");
  const [prompt, setPrompt] = useState("");
  const [title, setTitle] = useState("");
  const [settings, setSettings] = useState(false);
  const [envs, setEnvs] = useState<DevEnv[]>([]);
  const [envOutput, setEnvOutput] = useState("");
  const [usage, setUsage] = useState<UsageInterval[]>([]);
  const [usageTotal, setUsageTotal] = useState(0n);
  const [participants, setParticipants] = useState<string[]>([]);
  const [memory, setMemory] = useState<{ key: string; valueJson: string }[]>([]);
  const [credential, setCredential] = useState<CredentialForm>({ apiKey: "", baseUrl: "", extraHeaders: "{}" });
  const [provider, setProvider] = useState<ProviderForm>({ kind: "byo_k8s", name: "", config: '{"mode":"loopback"}' });
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const listed = await api.orgs.listOrgs({});
      setOrgs(listed.orgs);
      const selected = org ?? listed.orgs[0];
      setOrg(selected);
      if (selected) setSessions((await api.sessions.listSessions({ orgId: selected.id })).sessions);
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

  const refreshMultiplayer = useCallback(async () => {
    if (!session) return;
    const p = await api.sessions.listParticipants({ sessionId: session.id });
    setParticipants(p.participants.filter((x) => x.state === "active").map((x) => x.display || x.participantId));
    const m = await api.sessions.listMemory({ sessionId: session.id });
    setMemory(m.entries);
  }, [api, session]);

  useEffect(() => {
    if (!session) return;
    void api.sessions.join({ sessionId: session.id, display: "You" }).then(refreshMultiplayer);
    void refreshEnvs();
    void refreshUsage();
  }, [session, api, refreshMultiplayer, refreshEnvs, refreshUsage]);

  useEffect(() => {
    if (!session) return;
    const abort = new AbortController();
    setConnection("connecting");
    (async () => {
      try {
        for await (const resp of api.events.subscribe({ sessionId: session.id, fromSeq: 0n }, { signal: abort.signal })) {
          setConnection("live");
          if (resp.event) dispatch(resp.event);
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
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }
  function changeToken(value: string) {
    localStorage.setItem("ultra-token", value);
    setToken(value);
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
            <EnvironmentPanel
              envs={envs}
              output={envOutput}
              onProvision={provisionEnv}
              onRestart={restartEnv}
              onTerminate={terminateEnv}
              onExec={execPreview}
            />
            <UsagePanel intervals={usage} totalSeconds={usageTotal} onRefresh={refreshUsage} />
            {memory.length > 0 && (
              <details className="text-xs" data-testid="session-memory">
                <summary>Session memory ({memory.length})</summary>
                {memory.map((m) => (
                  <pre key={m.key}>
                    {m.key}: {m.valueJson}
                  </pre>
                ))}
              </details>
            )}
            <Timeline items={view.items} onAnswer={answer} deltaFrames={view.deltaFrames} />
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
