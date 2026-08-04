import { useEffect, useState, type FormEvent, type KeyboardEvent } from "react";
import { Button, Input } from "./ui";

/**
 * Debounced search with explicit submit. Rapid typing cancels prior scheduled
 * updates; parent still aborts in-flight RPCs via useCollection.
 */
export function SearchBar({
  value,
  onChange,
  placeholder = "Search…",
  debounceMs = 300,
  disabled,
}: {
  value: string;
  onChange: (q: string) => void;
  placeholder?: string;
  debounceMs?: number;
  disabled?: boolean;
}) {
  const [local, setLocal] = useState(value);

  useEffect(() => {
    setLocal(value);
  }, [value]);

  useEffect(() => {
    if (local === value) return;
    const t = window.setTimeout(() => onChange(local), debounceMs);
    return () => window.clearTimeout(t);
  }, [local, value, onChange, debounceMs]);

  function submit(e?: FormEvent) {
    e?.preventDefault();
    onChange(local);
  }

  function onKey(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Enter") {
      e.preventDefault();
      onChange(local);
    }
  }

  return (
    <form className="flex min-w-[16rem] flex-1 items-center gap-2" onSubmit={submit} role="search">
      <Input
        data-testid="search-bar"
        value={local}
        onChange={(e) => setLocal(e.target.value)}
        onKeyDown={onKey}
        placeholder={placeholder}
        disabled={disabled}
        aria-label="Search"
      />
      <Button type="submit" variant="secondary" size="sm" disabled={disabled}>
        Search
      </Button>
      {local ? (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => {
            setLocal("");
            onChange("");
          }}
        >
          Clear
        </Button>
      ) : null}
    </form>
  );
}
