import { useCallback, useEffect, useMemo, useReducer, useState } from "react";
import type { Org } from "@client/gen/ultra/v1/org_pb";
import type { Session } from "@client/gen/ultra/v1/session_pb";
import type { DevEnv } from "@client/gen/ultra/v1/env_pb";
import { clients } from "./api";
import { foldEvent, initialView, type TimelineItem } from "./reducer";

const baseUrl = import.meta.env.VITE_ULTRAD_URL ?? "http://localhost:8080";
const initialToken = localStorage.getItem("ultra-token") ?? "dev-token";

export function App() {
  const [token, setToken] = useState(initialToken);
  const api = useMemo(() => clients(baseUrl, token), [token]);
  const [orgs, setOrgs] = useState<Org[]>([]);
  const [org, setOrg] = useState<Org>();
  const [sessions, setSessions] = useState<Session[]>([]);
  const [session, setSession] = useState<Session>();
  const [view, dispatch] = useReducer(foldEvent, initialView);
  const [prompt, setPrompt] = useState("");
  const [title, setTitle] = useState("");
  const [settings, setSettings] = useState(false);
  const [envs, setEnvs] = useState<DevEnv[]>([]);
  const [command, setCommand] = useState("");
  const [envOutput, setEnvOutput] = useState("");
  const [key, setKey] = useState("");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const listed = await api.orgs.listOrgs({});
      setOrgs(listed.orgs);
      const selected = org ?? listed.orgs[0];
      setOrg(selected);
      if (selected) setSessions((await api.sessions.listSessions({ orgId: selected.id })).sessions);
      setError("");
    } catch (e) { setError(String(e)); }
  }, [api, org]);
  useEffect(() => { void load(); }, [load]);

  useEffect(() => { void refreshEnvs(); }, [session]);

  useEffect(() => {
    if (!session) return;
    const abort = new AbortController();
    (async () => {
      try {
        for await (const resp of api.events.subscribe({ sessionId: session.id, fromSeq: 0n }, { signal: abort.signal })) {
          if (resp.event) dispatch(resp.event);
        }
      } catch (e) { if (!abort.signal.aborted) setError(String(e)); }
    })();
    return () => abort.abort();
  }, [api, session]);

  async function createSession() {
    if (!org) return;
    const created = await api.sessions.createSession({ orgId: org.id, title: title || "New session" });
    if (created.session) { setSessions((s) => [created.session!, ...s]); setSession(created.session); }
    setTitle("");
  }
  async function sendPrompt() {
    if (!session || !prompt.trim()) return;
    await api.events.append({ sessionId: session.id, payload: { payload: { case: "userMessage", value: { text: prompt } } } });
    await api.agents.startRun({ sessionId: session.id, prompt });
    setPrompt("");
  }
  async function answer(runId: string, message: string) { await api.agents.promptRun({ runId, message }); }
  async function refreshEnvs() { if(session) setEnvs((await api.envs.listEnvs({sessionId:session.id})).envs); }
  async function provisionEnv() { if(!session)return; await api.envs.provisionEnv({sessionId:session.id,spec:{name:"main",workdir:"/work",env:{},metadata:{}},providerInstance:"default"}); await refreshEnvs(); setTimeout(refreshEnvs,1000); }
  async function execPreview() { const env=envs.find(e=>e.state===3);if(!env||!command)return;const r=await api.envs.execPreview({envId:env.id,command});setEnvOutput(r.output);setCommand(""); }
  async function saveKey() {
    if (!org || !key) return;
    await api.orgs.putCredential({ orgId: org.id, kind: "inference:openai", name: "default", apiKey: key });
    setKey(""); setSettings(false);
  }
  function changeToken(value: string) { localStorage.setItem("ultra-token", value); setToken(value); }

  return <div className="flex min-h-screen bg-zinc-950 text-zinc-100">
    <aside className="w-72 border-r border-zinc-800 p-4 flex flex-col gap-4">
      <h1 className="font-semibold tracking-tight text-xl">Ultralogical</h1>
      <select aria-label="Organization" className="bg-zinc-900 border border-zinc-700 rounded px-2 py-2" value={org?.id ?? ""}
        onChange={(e) => setOrg(orgs.find((o) => o.id === e.target.value))}>
        {orgs.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
      </select>
      <div className="flex gap-2"><input aria-label="New session title" className="min-w-0 flex-1 bg-zinc-900 border border-zinc-700 rounded px-2" value={title} onChange={(e) => setTitle(e.target.value)} /><button className="bg-white text-black rounded px-3 py-2" onClick={createSession}>+</button></div>
      <nav className="flex flex-col gap-1 overflow-auto">
        {sessions.map((s) => <button key={s.id} onClick={() => setSession(s)} className={`text-left rounded px-3 py-2 ${session?.id === s.id ? "bg-zinc-800" : "hover:bg-zinc-900"}`}>{s.title || "Untitled"}</button>)}
      </nav>
      <div className="mt-auto flex gap-2"><button className="text-sm text-zinc-400" onClick={() => setSettings(!settings)}>Settings</button><input aria-label="API token" className="w-28 text-xs bg-zinc-900 border border-zinc-800 rounded px-2" value={token} onChange={(e) => changeToken(e.target.value)} /></div>
    </aside>
    <main className="flex-1 max-w-4xl mx-auto p-6 flex flex-col h-screen">
      {settings ? <section className="space-y-4"><h2 className="text-2xl font-semibold">Org settings</h2><p className="text-zinc-400">Inference credentials are write-only and encrypted at rest.</p><input aria-label="OpenAI API key" type="password" value={key} onChange={(e) => setKey(e.target.value)} className="w-full bg-zinc-900 border border-zinc-700 rounded p-3" /><button onClick={saveKey} className="bg-white text-black rounded px-4 py-2">Save credential</button></section>
      : session ? <><header className="border-b border-zinc-800 pb-4 flex justify-between"><h2 className="text-xl font-medium">{session.title}</h2><button onClick={provisionEnv} className="border border-zinc-700 rounded px-3 text-sm">New environment</button></header><div className="py-2 flex gap-2 items-center">{envs.map(e=><span key={e.id} className="text-xs border border-zinc-700 rounded px-2 py-1">{e.spec?.name}: {e.state}</span>)}<input aria-label="Environment command" value={command} onChange={e=>setCommand(e.target.value)} className="ml-auto bg-zinc-900 border border-zinc-700 rounded px-2"/><button onClick={execPreview} className="text-sm border rounded px-2">Run</button></div>{envOutput&&<pre data-testid="env-output" className="text-xs bg-zinc-900 p-2">{envOutput}</pre>}<section data-testid="timeline" className="flex-1 overflow-auto py-4 space-y-3">{view.items.map((item, i) => <Timeline key={i} item={item} onAnswer={answer} />)}</section><div className="flex gap-2 border-t border-zinc-800 pt-4"><input aria-label="Prompt" value={prompt} onChange={(e) => setPrompt(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") void sendPrompt(); }} className="flex-1 bg-zinc-900 border border-zinc-700 rounded p-3" placeholder="Ask an agent…" /><button onClick={sendPrompt} className="bg-white text-black rounded px-5">Send</button></div></> : <div className="m-auto text-zinc-500">Create or select a session</div>}
      {error && <div role="alert" className="fixed right-4 bottom-4 bg-red-950 border border-red-800 rounded p-3 text-sm">{error}</div>}
    </main>
  </div>;
}

function Timeline({ item, onAnswer }: { item: TimelineItem; onAnswer: (runId: string, message: string) => Promise<void> }) {
  switch (item.type) {
    case "user": return <div className="ml-auto max-w-xl bg-zinc-800 rounded-xl p-3" data-kind="user">{item.text}</div>;
    case "assistant": return <div className="max-w-2xl whitespace-pre-wrap" data-kind="assistant">{item.text}{item.streaming && <span className="animate-pulse">▍</span>}</div>;
    case "tool": return <details className="border border-zinc-800 bg-zinc-900 rounded p-3" data-kind="tool"><summary className="cursor-pointer font-mono text-sm">{item.name}</summary><pre className="text-xs overflow-auto text-zinc-400 mt-2">{item.input}{item.output && `\n→ ${item.output}`}</pre></details>;
    case "question": return <div className="border border-amber-800 bg-amber-950/30 rounded p-4 space-y-3" data-kind="question"><p>{item.text}</p><div className="flex gap-2">{item.choices.map((c) => <button key={c} onClick={() => onAnswer(item.runId, c)} className="border border-amber-700 rounded px-3 py-1">{c}</button>)}<AnswerForm onAnswer={(v) => onAnswer(item.runId, v)} /></div></div>;
    case "status": return <div className="text-xs uppercase tracking-wide text-zinc-500" data-status={item.status}>{item.status}{item.message && `: ${item.message}`}</div>;
    case "annotation": return <div className="text-sm italic text-zinc-400">Note: {item.text}</div>;
  }
}

function AnswerForm({ onAnswer }: { onAnswer: (v: string) => Promise<void> }) {
  const [value, setValue] = useState("");
  return <><input aria-label="Answer" value={value} onChange={(e) => setValue(e.target.value)} className="bg-zinc-900 border border-zinc-700 rounded px-2" /><button onClick={() => onAnswer(value)} className="bg-amber-700 rounded px-3">Answer</button></>;
}
