import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

export function MemoryPanel({
  entries,
  onSet,
}: {
  entries: { key: string; valueJson: string }[];
  onSet: (key: string, valueJson: string) => Promise<void>;
}) {
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  return (
    <Card data-testid="session-memory">
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle>Session memory</CardTitle>
        <span className="text-xs text-zinc-500" data-testid="memory-count">
          {entries.length} entries
        </span>
      </CardHeader>
      <CardContent className="space-y-3">
        <ul className="space-y-1">
          {entries.map((entry) => (
            <li
              key={entry.key}
              data-testid="memory-entry"
              data-key={entry.key}
              className="font-mono text-xs text-zinc-400"
            >
              {entry.key}: {entry.valueJson}
            </li>
          ))}
          {entries.length === 0 && <li className="text-xs text-zinc-500">No memory entries yet</li>}
        </ul>
        <div className="flex gap-2">
          <Input aria-label="Memory key" value={key} onChange={(e) => setKey(e.target.value)} placeholder="note" />
          <Input
            aria-label="Memory value"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder='"remembered"'
          />
          <Button
            size="sm"
            variant="outline"
            onClick={async () => {
              await onSet(key, value);
              setKey("");
              setValue("");
            }}
          >
            Remember
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
