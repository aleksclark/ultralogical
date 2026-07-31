import { useState } from "react";
import type { DevEnv } from "@client/gen/ultra/v1/env_pb";
import { EnvState } from "@client/gen/ultra/v1/env_pb";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

const phases: Record<number, string> = {
  [EnvState.REQUESTED]: "requested",
  [EnvState.PROVISIONING]: "provisioning",
  [EnvState.READY]: "ready",
  [EnvState.SUSPENDED]: "suspended",
  [EnvState.TERMINATING]: "terminating",
  [EnvState.TERMINATED]: "terminated",
  [EnvState.FAILED]: "failed",
};

export function phaseLabel(state: EnvState): string {
  return phases[state] ?? "unknown";
}

function phaseVariant(state: EnvState) {
  if (state === EnvState.READY) return "success" as const;
  if (state === EnvState.FAILED) return "destructive" as const;
  if (state === EnvState.TERMINATED) return "default" as const;
  return "pending" as const;
}

export function EnvironmentPanel({
  envs,
  output,
  onProvision,
  onRestart,
  onTerminate,
  onExec,
}: {
  envs: DevEnv[];
  output: string;
  onProvision: () => Promise<void>;
  onRestart: (envId: string) => Promise<void>;
  onTerminate: (envId: string) => Promise<void>;
  onExec: (command: string) => Promise<void>;
}) {
  const [command, setCommand] = useState("");
  return (
    <Card data-testid="environment-panel">
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle>Environments</CardTitle>
        <Button size="sm" variant="outline" onClick={onProvision}>
          New environment
        </Button>
      </CardHeader>
      <CardContent className="space-y-3">
        <ul className="flex flex-wrap gap-2">
          {envs.map((env) => (
            <li key={env.id} data-testid="environment-chip" data-env-id={env.id} data-epoch={env.epoch} className="flex items-center gap-2">
              <Badge variant={phaseVariant(env.state)} data-phase={phaseLabel(env.state)}>
                {env.spec?.name}: {phaseLabel(env.state)}
              </Badge>
              <span className="text-xs text-zinc-500" data-testid="environment-epoch">
                epoch {env.epoch}
              </span>
              <Button
                size="sm"
                variant="ghost"
                aria-label={`Restart ${env.spec?.name}`}
                onClick={() => onRestart(env.id)}
              >
                Restart
              </Button>
              <Button
                size="sm"
                variant="ghost"
                aria-label={`Terminate ${env.spec?.name}`}
                onClick={() => onTerminate(env.id)}
              >
                Terminate
              </Button>
            </li>
          ))}
          {envs.length === 0 && <li className="text-xs text-zinc-500">No environments yet</li>}
        </ul>
        <div className="flex gap-2">
          <Input
            aria-label="Environment command"
            value={command}
            onChange={(e) => setCommand(e.target.value)}
            placeholder="echo hello"
          />
          <Button
            size="sm"
            variant="outline"
            onClick={async () => {
              await onExec(command);
              setCommand("");
            }}
          >
            Run
          </Button>
        </div>
        {output && (
          <pre data-testid="env-output" className="max-h-40 overflow-auto rounded bg-zinc-900 p-2 text-xs text-zinc-300">
            {output}
          </pre>
        )}
      </CardContent>
    </Card>
  );
}
