import { useMemo, useState } from "react";
import {
  deleteSavedView,
  listSavedViews,
  saveView,
  type SavedView,
} from "@/data/savedViews";
import type { QueryState } from "@/query/types";
import { Button, Input, Select } from "./ui";

export function SavedViews({
  collection,
  state,
  onApply,
}: {
  collection: string;
  state: QueryState;
  onApply: (view: SavedView) => void;
}) {
  const [name, setName] = useState("");
  const [rev, setRev] = useState(0);
  const views = useMemo(() => {
    void rev;
    return listSavedViews(collection);
  }, [collection, rev]);

  function save() {
    const n = name.trim() || `View ${new Date().toLocaleString()}`;
    saveView({
      name: n,
      collection,
      q: state.q,
      filters: state.filters,
      sorts: state.sorts,
      limit: state.limit,
      columns: state.columns,
      tenantId: state.tenantId,
    });
    setName("");
    setRev((r) => r + 1);
  }

  return (
    <div className="flex flex-wrap items-center gap-2" data-testid="saved-views">
      <Select
        aria-label="Saved views"
        value={state.viewId || ""}
        onChange={(e) => {
          const id = e.target.value;
          if (!id) return;
          const v = views.find((x) => x.id === id);
          if (v) onApply(v);
        }}
      >
        <option value="">Saved views…</option>
        {views.map((v) => (
          <option key={v.id} value={v.id}>
            {v.name}
          </option>
        ))}
      </Select>
      <Input
        className="w-40"
        placeholder="Name view"
        value={name}
        onChange={(e) => setName(e.target.value)}
        aria-label="Saved view name"
      />
      <Button type="button" size="sm" variant="secondary" onClick={save}>
        Save view
      </Button>
      {state.viewId ? (
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            deleteSavedView(state.viewId);
            setRev((r) => r + 1);
          }}
        >
          Delete
        </Button>
      ) : null}
    </div>
  );
}
