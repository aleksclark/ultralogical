import { useMemo, useState } from "react";
import { safeStringify, toPlain } from "@/lib/format";
import { Button, Input } from "./ui";

const MAX_RENDER_CHARS = 200_000;

export function JsonViewer({
  value,
  title = "JSON",
  defaultCollapsed = false,
  maxChars = MAX_RENDER_CHARS,
}: {
  value: unknown;
  title?: string;
  defaultCollapsed?: boolean;
  maxChars?: number;
}) {
  const [open, setOpen] = useState(!defaultCollapsed);
  const [filter, setFilter] = useState("");
  const plain = useMemo(() => toPlain(value), [value]);
  const text = useMemo(() => safeStringify(plain, 2), [plain]);
  const truncated = text.length > maxChars;
  const display = truncated ? `${text.slice(0, maxChars)}\n… [truncated ${text.length - maxChars} chars]` : text;

  const filtered = useMemo(() => {
    if (!filter.trim()) return display;
    const lines = display.split("\n");
    const q = filter.toLowerCase();
    return lines.filter((l) => l.toLowerCase().includes(q)).join("\n") || "(no matching lines)";
  }, [display, filter]);

  function copy() {
    void navigator.clipboard?.writeText(text);
  }

  function download() {
    const blob = new Blob([text], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${title.replace(/\s+/g, "_").toLowerCase()}.json`;
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div className="rounded-md border bg-background/60" data-testid="json-viewer">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b px-3 py-2">
        <button
          type="button"
          className="text-left text-xs font-medium"
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
        >
          {open ? "▼" : "▶"} {title}
          {truncated ? (
            <span className="ml-2 text-muted-foreground">(large — truncated in view)</span>
          ) : null}
        </button>
        <div className="flex items-center gap-2">
          {open ? (
            <Input
              className="h-7 w-40 text-xs"
              placeholder="Filter lines…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              aria-label="Filter JSON lines"
            />
          ) : null}
          <Button type="button" size="sm" variant="ghost" onClick={copy}>
            Copy
          </Button>
          <Button type="button" size="sm" variant="ghost" onClick={download}>
            Download
          </Button>
        </div>
      </div>
      {open ? (
        <pre className="max-h-[28rem] overflow-auto p-3 font-mono text-[11px] leading-relaxed text-muted-foreground">
          {filtered}
        </pre>
      ) : null}
    </div>
  );
}
