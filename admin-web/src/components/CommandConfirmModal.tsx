import { useState } from "react";
import {
  executeCommand,
  newIdempotencyKey,
  previewCommand,
  type CommandArgs,
  type CommandName,
} from "@/data/commands";
import { useAdminCommandClient } from "@/lib/client";
import { useAuth } from "@/lib/auth";
import { useOperator } from "@/lib/operator";
import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Input,
  Label,
  Textarea,
} from "./ui";

type Props = {
  open: boolean;
  onClose: () => void;
  command: CommandName;
  args: CommandArgs;
  title: string;
  /** When true, require typing the phrase to enable confirm (bulk/destructive). */
  confirmPhrase?: string;
  /** Reveal requires reauth token field. */
  requireReauth?: boolean;
  onDone?: (result: {
    outcomeResult: string;
    plaintext?: string;
    evidenceJson?: Uint8Array;
  }) => void;
};

export function CommandConfirmModal({
  open,
  onClose,
  command,
  args,
  title,
  confirmPhrase,
  requireReauth,
  onDone,
}: Props) {
  const client = useAdminCommandClient();
  const { token } = useAuth();
  const { can } = useOperator();
  const [reason, setReason] = useState("");
  const [phrase, setPhrase] = useState("");
  const [reauth, setReauth] = useState("");
  const [previewHash, setPreviewHash] = useState("");
  const [effects, setEffects] = useState<string[]>([]);
  const [before, setBefore] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [step, setStep] = useState<"edit" | "preview" | "done">("edit");
  const [resultMsg, setResultMsg] = useState("");
  const [plaintext, setPlaintext] = useState<string | null>(null);

  if (!open) return null;

  const allowed = can(command);

  async function doPreview() {
    setBusy(true);
    setError(null);
    try {
      const outcome = await previewCommand(client, command, args, reason || "preview");
      setPreviewHash(outcome.preview?.previewHash ?? "");
      setEffects(outcome.preview?.expectedEffects ?? []);
      setBefore(JSON.stringify(outcome.preview?.beforeSummary ?? {}, null, 2));
      setStep("preview");
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function doExecute() {
    setBusy(true);
    setError(null);
    try {
      const res = await executeCommand(
        client,
        command,
        args,
        reason,
        previewHash,
        newIdempotencyKey(),
        requireReauth ? reauth || token || undefined : undefined,
      );
      setResultMsg(res.outcome.result || "ok");
      if (res.plaintext) setPlaintext(res.plaintext);
      setStep("done");
      onDone?.({
        outcomeResult: res.outcome.result,
        plaintext: res.plaintext,
        evidenceJson: res.evidenceJson,
      });
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  const phraseOk = !confirmPhrase || phrase === confirmPhrase;
  const reasonOk = reason.trim().length > 0;
  const reauthOk = !requireReauth || reauth.trim().length > 0;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      data-testid="command-confirm-modal"
      role="dialog"
      aria-modal="true"
    >
      <Card className="max-h-[90vh] w-full max-w-lg overflow-auto">
        <CardHeader>
          <CardTitle>{title}</CardTitle>
          <p className="text-xs text-muted-foreground font-mono">{command}</p>
        </CardHeader>
        <CardContent className="space-y-3">
          {!allowed && (
            <div className="rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
              Your role cannot run this command.
            </div>
          )}
          {requireReauth && (
            <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-xs text-amber-200">
              Break-glass reveal: plaintext is shown once, never stored, never auto-copied.
            </div>
          )}
          <div className="space-y-1">
            <Label htmlFor="cmd-reason">Reason (required)</Label>
            <Textarea
              id="cmd-reason"
              data-testid="command-reason"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              rows={2}
              disabled={step === "done"}
            />
          </div>
          {requireReauth && step !== "done" && (
            <div className="space-y-1">
              <Label htmlFor="cmd-reauth">Re-auth token</Label>
              <Input
                id="cmd-reauth"
                data-testid="command-reauth"
                type="password"
                value={reauth}
                onChange={(e) => setReauth(e.target.value)}
                placeholder="paste operator token again"
                autoComplete="off"
              />
            </div>
          )}
          {confirmPhrase && step === "preview" && (
            <div className="space-y-1">
              <Label htmlFor="cmd-phrase">Type “{confirmPhrase}” to confirm</Label>
              <Input
                id="cmd-phrase"
                data-testid="command-phrase"
                value={phrase}
                onChange={(e) => setPhrase(e.target.value)}
              />
            </div>
          )}
          {step !== "edit" && (
            <div className="space-y-2 rounded-md border bg-muted/30 p-2 text-xs">
              <div className="font-semibold">Expected effects</div>
              <ul className="list-disc pl-4">
                {effects.map((e) => (
                  <li key={e}>{e}</li>
                ))}
              </ul>
              <div className="font-semibold">Before</div>
              <pre className="max-h-40 overflow-auto whitespace-pre-wrap font-mono text-[10px]">
                {before}
              </pre>
              <div className="font-mono text-[10px] text-muted-foreground">
                preview_hash: {previewHash.slice(0, 16)}…
              </div>
            </div>
          )}
          {plaintext !== null && (
            <div
              className="rounded-md border border-destructive/50 bg-destructive/10 p-2"
              data-testid="reveal-plaintext"
            >
              <div className="mb-1 text-xs font-semibold text-destructive">
                Secret (not stored — copy manually if needed)
              </div>
              <pre className="max-h-32 overflow-auto whitespace-pre-wrap break-all font-mono text-xs">
                {plaintext}
              </pre>
            </div>
          )}
          {step === "done" && (
            <div className="text-sm" data-testid="command-result">
              Result: <span className="font-mono">{resultMsg}</span>
            </div>
          )}
          {error && (
            <div className="text-xs text-destructive" data-testid="command-error">
              {error}
            </div>
          )}
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="ghost" onClick={onClose} disabled={busy}>
              Close
            </Button>
            {step === "edit" && (
              <Button
                data-testid="command-preview"
                onClick={doPreview}
                disabled={!allowed || busy || !reasonOk || (requireReauth && !reauthOk)}
              >
                Preview
              </Button>
            )}
            {step === "preview" && (
              <Button
                data-testid="command-execute"
                variant="destructive"
                onClick={doExecute}
                disabled={!allowed || busy || !reasonOk || !phraseOk || (requireReauth && !reauthOk)}
              >
                Confirm execute
              </Button>
            )}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
