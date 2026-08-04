/**
 * Admin command data layer — typed mutations with dry-run → confirm.
 * Not List* RPCs; confirmation UI imports from here only.
 */
import type { AdminCommandClient } from "@/lib/client";
import type { CommandOptions, CommandOutcome } from "@admin-gen/admin/v1/admin_pb";

export type CommandName =
  | "RetryQueueJob"
  | "CancelQueueJob"
  | "CancelRun"
  | "AnswerAwait"
  | "ExpireAwait"
  | "ResourceReconcile"
  | "ResourceRestart"
  | "ResourceSuspend"
  | "ResourceTerminate"
  | "ResourceAdoptionProbe"
  | "ReprobeProvider"
  | "RevokeAPIKey"
  | "DisableCredential"
  | "PausePeriodicPrompt"
  | "ResumePeriodicPrompt"
  | "DisconnectSubscriber"
  | "ExportIncidentEvidence"
  | "RevealSecret";

export type CommandArgs = {
  jobId?: bigint | number;
  runId?: string;
  message?: string;
  resourceId?: string;
  providerId?: string;
  apiKeyId?: string;
  tenantId?: string;
  kind?: string;
  name?: string;
  periodicPromptId?: string;
  sessionId?: string;
  subscriberId?: string;
  maxEvents?: number;
  secretKind?: string;
  credentialKind?: string;
  credentialName?: string;
};

function opts(partial: Partial<CommandOptions> & { reason: string }): CommandOptions {
  return {
    $typeName: "admin.v1.CommandOptions",
    dryRun: Boolean(partial.dryRun),
    previewHash: partial.previewHash ?? "",
    idempotencyKey: partial.idempotencyKey ?? "",
    reason: partial.reason,
  } as CommandOptions;
}

export async function previewCommand(
  client: AdminCommandClient,
  command: CommandName,
  args: CommandArgs,
  reason: string,
): Promise<CommandOutcome> {
  return invoke(client, command, args, opts({ dryRun: true, reason }));
}

export async function executeCommand(
  client: AdminCommandClient,
  command: CommandName,
  args: CommandArgs,
  reason: string,
  previewHash: string,
  idempotencyKey: string,
  reauthToken?: string,
): Promise<{ outcome: CommandOutcome; plaintext?: string; evidenceJson?: Uint8Array }> {
  const o = opts({ dryRun: false, reason, previewHash, idempotencyKey });
  return invokeFull(client, command, args, o, reauthToken);
}

async function invoke(
  client: AdminCommandClient,
  command: CommandName,
  args: CommandArgs,
  options: CommandOptions,
): Promise<CommandOutcome> {
  const r = await invokeFull(client, command, args, options);
  return r.outcome;
}

async function invokeFull(
  client: AdminCommandClient,
  command: CommandName,
  args: CommandArgs,
  options: CommandOptions,
  reauthToken?: string,
): Promise<{ outcome: CommandOutcome; plaintext?: string; evidenceJson?: Uint8Array }> {
  const headers = reauthToken ? { "X-Admin-Reauth": reauthToken } : undefined;
  // connect-es accepts headers via call options in second arg for some versions;
  // transport interceptor is preferred for reauth — pass via custom call.
  const callOpts = headers
    ? {
        headers,
      }
    : undefined;

  switch (command) {
    case "RetryQueueJob": {
      const res = await client.retryQueueJob(
        { options, jobId: BigInt(args.jobId ?? 0) },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "CancelQueueJob": {
      const res = await client.cancelQueueJob(
        { options, jobId: BigInt(args.jobId ?? 0) },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "CancelRun": {
      const res = await client.cancelRun({ options, runId: args.runId ?? "" }, callOpts);
      return { outcome: res.outcome! };
    }
    case "AnswerAwait": {
      const res = await client.answerAwait(
        { options, runId: args.runId ?? "", message: args.message ?? "" },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "ExpireAwait": {
      const res = await client.expireAwait({ options, runId: args.runId ?? "" }, callOpts);
      return { outcome: res.outcome! };
    }
    case "ResourceReconcile": {
      const res = await client.resourceReconcile(
        { options, resourceId: args.resourceId ?? "" },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "ResourceRestart": {
      const res = await client.resourceRestart(
        { options, resourceId: args.resourceId ?? "" },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "ResourceSuspend": {
      const res = await client.resourceSuspend(
        { options, resourceId: args.resourceId ?? "", message: args.message ?? "" },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "ResourceTerminate": {
      const res = await client.resourceTerminate(
        { options, resourceId: args.resourceId ?? "" },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "ResourceAdoptionProbe": {
      const res = await client.resourceAdoptionProbe(
        { options, resourceId: args.resourceId ?? "" },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "ReprobeProvider": {
      const res = await client.reprobeProvider(
        { options, providerId: args.providerId ?? "" },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "RevokeAPIKey": {
      const res = await client.revokeAPIKey(
        { options, apiKeyId: args.apiKeyId ?? "" },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "DisableCredential": {
      const res = await client.disableCredential(
        {
          options,
          tenantId: args.tenantId ?? "",
          kind: args.kind ?? "",
          name: args.name ?? "",
        },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "PausePeriodicPrompt": {
      const res = await client.pausePeriodicPrompt(
        { options, periodicPromptId: args.periodicPromptId ?? "" },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "ResumePeriodicPrompt": {
      const res = await client.resumePeriodicPrompt(
        { options, periodicPromptId: args.periodicPromptId ?? "" },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "DisconnectSubscriber": {
      const res = await client.disconnectSubscriber(
        {
          options,
          sessionId: args.sessionId ?? "",
          subscriberId: args.subscriberId ?? "",
        },
        callOpts,
      );
      return { outcome: res.outcome! };
    }
    case "ExportIncidentEvidence": {
      const res = await client.exportIncidentEvidence(
        {
          options,
          sessionId: args.sessionId ?? "",
          runId: args.runId ?? "",
          resourceId: args.resourceId ?? "",
          maxEvents: args.maxEvents ?? 100,
        },
        callOpts,
      );
      return { outcome: res.outcome!, evidenceJson: res.evidenceJson };
    }
    case "RevealSecret": {
      const res = await client.revealSecret(
        {
          options,
          secretKind: args.secretKind ?? "api_key",
          apiKeyId: args.apiKeyId ?? "",
          tenantId: args.tenantId ?? "",
          credentialKind: args.credentialKind ?? "",
          credentialName: args.credentialName ?? "",
        },
        callOpts,
      );
      return { outcome: res.outcome!, plaintext: res.plaintext };
    }
    default: {
      const _e: never = command;
      throw new Error(`unknown command ${_e}`);
    }
  }
}

export function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `idem-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
