import { useState } from "react";
import type { FilterOp, QueryFilter } from "@/query/types";
import { Badge, Button, Input, Select } from "./ui";

export type FilterFieldMeta = {
  name: string;
  label?: string;
  ops: FilterOp[];
};

const OP_LABELS: Record<FilterOp, string> = {
  eq: "=",
  ne: "≠",
  lt: "<",
  lte: "≤",
  gt: ">",
  gte: "≥",
  in: "in",
  not_in: "not in",
  contains: "contains",
  prefix: "prefix",
  is_null: "is null",
  is_not_null: "is not null",
};

const DEFAULT_OPS: FilterOp[] = ["eq", "ne", "contains", "prefix", "in"];

export function FilterBuilder({
  fields,
  filters,
  onChange,
}: {
  fields: FilterFieldMeta[];
  filters: QueryFilter[];
  onChange: (filters: QueryFilter[]) => void;
}) {
  const [field, setField] = useState(fields[0]?.name ?? "");
  const [op, setOp] = useState<FilterOp>("eq");
  const [value, setValue] = useState("");

  const meta = fields.find((f) => f.name === field);
  const ops = meta?.ops?.length ? meta.ops : DEFAULT_OPS;

  function add() {
    if (!field) return;
    if (op !== "is_null" && op !== "is_not_null" && !value.trim()) return;
    const next: QueryFilter =
      op === "in" || op === "not_in"
        ? { field, op, values: value.split(",").map((s) => s.trim()).filter(Boolean) }
        : op === "is_null" || op === "is_not_null"
          ? { field, op }
          : { field, op, value: value.trim() };
    onChange([...filters, next]);
    setValue("");
  }

  function remove(i: number) {
    onChange(filters.filter((_, idx) => idx !== i));
  }

  return (
    <div className="space-y-2" data-testid="filter-builder">
      <div className="flex flex-wrap items-end gap-2">
        <div className="flex flex-col gap-1">
          <span className="text-[11px] text-muted-foreground">Field</span>
          <Select value={field} onChange={(e) => setField(e.target.value)} aria-label="Filter field">
            {fields.map((f) => (
              <option key={f.name} value={f.name}>
                {f.label ?? f.name}
              </option>
            ))}
          </Select>
        </div>
        <div className="flex flex-col gap-1">
          <span className="text-[11px] text-muted-foreground">Op</span>
          <Select
            value={op}
            onChange={(e) => setOp(e.target.value as FilterOp)}
            aria-label="Filter operator"
          >
            {ops.map((o) => (
              <option key={o} value={o}>
                {OP_LABELS[o]}
              </option>
            ))}
          </Select>
        </div>
        {op !== "is_null" && op !== "is_not_null" ? (
          <div className="flex min-w-[12rem] flex-1 flex-col gap-1">
            <span className="text-[11px] text-muted-foreground">
              Value{op === "in" || op === "not_in" ? " (comma-separated)" : ""}
            </span>
            <Input
              value={value}
              onChange={(e) => setValue(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  add();
                }
              }}
              placeholder="value"
              aria-label="Filter value"
            />
          </div>
        ) : null}
        <Button type="button" size="sm" variant="secondary" onClick={add}>
          Add filter
        </Button>
      </div>
      {filters.length ? (
        <div className="flex flex-wrap gap-1.5">
          {filters.map((f, i) => (
            <Badge key={`${f.field}-${i}`} variant="outline" className="gap-1 font-mono text-[11px]">
              <span>
                {f.field} {OP_LABELS[f.op]}{" "}
                {f.op === "in" || f.op === "not_in"
                  ? (f.values ?? []).join("|")
                  : f.op === "is_null" || f.op === "is_not_null"
                    ? ""
                    : f.value}
              </span>
              <button
                type="button"
                className="ml-1 text-muted-foreground hover:text-foreground"
                onClick={() => remove(i)}
                aria-label={`Remove filter ${f.field}`}
              >
                ×
              </button>
            </Badge>
          ))}
          <Button type="button" size="sm" variant="ghost" onClick={() => onChange([])}>
            Clear filters
          </Button>
        </div>
      ) : null}
    </div>
  );
}
