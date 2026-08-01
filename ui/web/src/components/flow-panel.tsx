import { useEffect, useMemo, useState } from "react";
import type { Flow, FlowFieldError, FlowInvocation } from "@client/gen/ultra/v1/flow_pb";
import { FlowInvocationState } from "@client/gen/ultra/v1/flow_pb";
import { EnvState } from "@client/gen/ultra/v1/env_pb";
import { RunState } from "@client/gen/ultra/v1/agent_pb";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";

const invocationStates: Record<number, string> = {
  [FlowInvocationState.PENDING]: "pending",
  [FlowInvocationState.PROVISIONING]: "provisioning",
  [FlowInvocationState.RUNNING]: "running",
  [FlowInvocationState.CANCELLING]: "cancelling",
  [FlowInvocationState.COMPLETED]: "completed",
  [FlowInvocationState.FAILED]: "failed",
  [FlowInvocationState.CANCELLED]: "cancelled",
};

const runStates: Record<number, string> = {
  [RunState.PENDING]: "pending",
  [RunState.RUNNING]: "running",
  [RunState.AWAITING]: "awaiting",
  [RunState.COMPLETED]: "completed",
  [RunState.FAILED]: "failed",
  [RunState.CANCELLED]: "cancelled",
};

const envStates: Record<number, string> = {
  [EnvState.REQUESTED]: "requested",
  [EnvState.PROVISIONING]: "provisioning",
  [EnvState.READY]: "ready",
  [EnvState.SUSPENDED]: "suspended",
  [EnvState.TERMINATING]: "terminating",
  [EnvState.TERMINATED]: "terminated",
  [EnvState.FAILED]: "failed",
};

export function invocationStateLabel(state: FlowInvocationState): string {
  return invocationStates[state] ?? "unknown";
}

function stateVariant(state: FlowInvocationState) {
  if (state === FlowInvocationState.COMPLETED) return "success" as const;
  if (state === FlowInvocationState.FAILED) return "destructive" as const;
  if (state === FlowInvocationState.CANCELLED) return "default" as const;
  return "pending" as const;
}

/** ParamField is one form field derived from a flow's declared parameters. */
type ParamField = {
  name: string;
  type: string;
  required: boolean;
  defaultValue: string;
  description: string;
};

/**
 * paramFields derives the invoke form from the selected version's own
 * definition. Deriving it rather than hard-coding fields is what makes the
 * form correct for any flow, including one authored a moment ago.
 */
export function paramFields(definitionJson: string): ParamField[] {
  try {
    const parsed = JSON.parse(definitionJson) as {
      params?: Record<string, { type?: string; required?: boolean; default?: unknown; description?: string }>;
    };
    return Object.entries(parsed.params ?? {})
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([name, spec]) => ({
        name,
        type: spec.type ?? "string",
        required: Boolean(spec.required),
        defaultValue: spec.default === undefined ? "" : String(spec.default),
        description: spec.description ?? "",
      }));
  } catch {
    return [];
  }
}

/** coerceParams converts form strings into the JSON types the flow declared. */
export function coerceParams(fields: ParamField[], values: Record<string, string>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const field of fields) {
    const raw = values[field.name] ?? field.defaultValue;
    if (raw === "" && !field.required) continue;
    if (field.type === "number") out[field.name] = Number(raw);
    else if (field.type === "boolean") out[field.name] = raw === "true";
    else out[field.name] = raw;
  }
  return out;
}

export function FlowPanel({
  flows,
  versions,
  selectedFlow,
  onSelectFlow,
  selectedVersion,
  onSelectVersion,
  definition,
  onDefinitionChange,
  validationErrors,
  onValidate,
  onSave,
  onInvoke,
  invocations,
  activeInvocation,
  onSelectInvocation,
  onCancelInvocation,
}: {
  flows: Flow[];
  versions: Flow[];
  selectedFlow?: string;
  onSelectFlow: (name: string) => void;
  selectedVersion: number;
  onSelectVersion: (version: number) => void;
  definition: string;
  onDefinitionChange: (value: string) => void;
  validationErrors: FlowFieldError[];
  onValidate: () => Promise<void>;
  onSave: (name: string) => Promise<void>;
  onInvoke: (params: Record<string, unknown>) => Promise<void>;
  invocations: FlowInvocation[];
  activeInvocation?: FlowInvocation;
  onSelectInvocation: (id: string) => void;
  onCancelInvocation: (id: string) => Promise<void>;
}) {
  const [draftName, setDraftName] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const selected = versions.find((f) => f.version === selectedVersion) ?? versions[0];
  const fields = useMemo(() => paramFields(selected?.definitionJson ?? ""), [selected?.definitionJson]);

  // Selecting a different version re-derives the form, so a parameter the new
  // version does not declare cannot linger in the request.
  useEffect(() => {
    setValues(Object.fromEntries(fields.map((field) => [field.name, field.defaultValue])));
  }, [fields]);

  return (
    <Card data-testid="flow-panel">
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle>Flows</CardTitle>
        <Badge data-testid="flow-count">{flows.length} in catalog</Badge>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex gap-2">
          <div className="flex-1">
            <Label htmlFor="flow-catalog">Flow</Label>
            <Select
              id="flow-catalog"
              aria-label="Flow"
              data-testid="flow-catalog"
              value={selectedFlow ?? ""}
              onChange={(e) => onSelectFlow(e.target.value)}
            >
              <option value="">Select a flow…</option>
              {flows.map((flow) => (
                <option key={flow.id} value={flow.name} data-testid="flow-option">
                  {flow.name} (latest v{flow.version})
                </option>
              ))}
            </Select>
          </div>
          <div className="w-40">
            <Label htmlFor="flow-version">Version</Label>
            <Select
              id="flow-version"
              aria-label="Flow version"
              data-testid="flow-version"
              value={String(selectedVersion)}
              onChange={(e) => onSelectVersion(Number(e.target.value))}
            >
              {versions.map((flow) => (
                <option key={flow.id} value={String(flow.version)}>
                  v{flow.version}
                </option>
              ))}
            </Select>
          </div>
        </div>

        <div className="space-y-1">
          <Label htmlFor="flow-definition">Definition</Label>
          <textarea
            id="flow-definition"
            aria-label="Flow definition"
            data-testid="flow-definition"
            className="h-32 w-full rounded-md border border-zinc-700 bg-zinc-900 p-2 font-mono text-xs text-zinc-100"
            value={definition}
            onChange={(e) => onDefinitionChange(e.target.value)}
          />
          <div className="flex items-center gap-2">
            <Input
              aria-label="Flow name"
              placeholder="flow name"
              value={draftName}
              onChange={(e) => setDraftName(e.target.value)}
            />
            <Button size="sm" variant="outline" onClick={onValidate}>
              Validate flow
            </Button>
            <Button size="sm" onClick={() => onSave(draftName)}>
              Save version
            </Button>
          </div>
        </div>

        {validationErrors.length > 0 && (
          <ul data-testid="flow-validation" className="space-y-1 rounded border border-red-900 bg-red-950/40 p-2">
            {validationErrors.map((error) => (
              <li
                key={`${error.path}:${error.code}`}
                data-testid="flow-validation-error"
                data-path={error.path}
                data-code={error.code}
                className="font-mono text-xs text-red-200"
              >
                {error.path}: {error.code}: {error.message}
              </li>
            ))}
          </ul>
        )}

        {selected && (
          <div className="space-y-2" data-testid="flow-invoke-form">
            {fields.map((field) => (
              <div key={field.name} className="flex items-center gap-2">
                <Label className="w-32" htmlFor={`param-${field.name}`}>
                  {field.name}
                  {field.required ? " *" : ""}
                </Label>
                <Input
                  id={`param-${field.name}`}
                  aria-label={`Parameter ${field.name}`}
                  data-testid="flow-param"
                  data-param={field.name}
                  data-type={field.type}
                  value={values[field.name] ?? ""}
                  onChange={(e) => setValues({ ...values, [field.name]: e.target.value })}
                />
              </div>
            ))}
            <Button size="sm" onClick={() => onInvoke(coerceParams(fields, values))}>
              Invoke flow
            </Button>
          </div>
        )}

        <ul className="flex flex-wrap gap-2" data-testid="flow-invocations">
          {invocations.map((invocation) => (
            <li key={invocation.id}>
              <Button
                size="sm"
                variant="ghost"
                data-testid="flow-invocation-chip"
                data-invocation-id={invocation.id}
                data-state={invocationStateLabel(invocation.state)}
                onClick={() => onSelectInvocation(invocation.id)}
              >
                {invocation.flowName} v{invocation.flowVersion}: {invocationStateLabel(invocation.state)}
              </Button>
            </li>
          ))}
        </ul>

        {activeInvocation && (
          <div
            className="space-y-2 rounded border border-zinc-800 p-2"
            data-testid="flow-invocation"
            data-invocation-id={activeInvocation.id}
            data-state={invocationStateLabel(activeInvocation.state)}
            data-terminal-reason={activeInvocation.terminalReason}
          >
            <div className="flex items-center justify-between">
              <div className="text-xs text-zinc-400" data-testid="flow-provenance">
                {activeInvocation.flowName} v{activeInvocation.flowVersion} · invocation{" "}
                {activeInvocation.id.slice(0, 8)} · flow {activeInvocation.flowId.slice(0, 8)}
              </div>
              <Badge variant={stateVariant(activeInvocation.state)} data-testid="flow-invocation-state">
                {invocationStateLabel(activeInvocation.state)}
              </Badge>
            </div>
            <ol data-testid="flow-progress" className="space-y-0.5">
              {activeInvocation.progress.map((entry) => (
                <li
                  key={`${entry.seq}`}
                  data-testid="flow-progress-entry"
                  data-stage={entry.stage}
                  data-key={entry.key}
                  className="font-mono text-xs text-zinc-400"
                >
                  {entry.seq}. {entry.stage} {entry.key} {entry.detail}
                </li>
              ))}
            </ol>
            <ul data-testid="flow-topology" className="space-y-0.5">
              {activeInvocation.envs.map((env) => (
                <li
                  key={env.envId}
                  data-testid="flow-env"
                  data-env-name={env.envName}
                  data-env-id={env.envId}
                  data-state={envStates[env.state] ?? "unknown"}
                  className="text-xs text-zinc-300"
                >
                  env {env.envName}: {envStates[env.state] ?? "unknown"} ({env.envId.slice(0, 8)})
                </li>
              ))}
              {activeInvocation.runs.map((run) => (
                <li
                  key={run.runId}
                  data-testid="flow-run"
                  data-agent={run.agentName}
                  data-run-id={run.runId}
                  data-state={runStates[run.state] ?? "unknown"}
                  data-parent-run-id={run.parentRunId}
                  className="text-xs text-zinc-300"
                >
                  agent {run.agentName}: {runStates[run.state] ?? "unknown"} ({run.runId.slice(0, 8)})
                </li>
              ))}
            </ul>
            <Button
              size="sm"
              variant="ghost"
              aria-label="Cancel invocation"
              onClick={() => onCancelInvocation(activeInvocation.id)}
            >
              Cancel invocation
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
