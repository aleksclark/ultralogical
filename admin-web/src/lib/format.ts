import type { Timestamp } from "@bufbuild/protobuf/wkt";

/** Format protobuf Timestamp or ISO-ish string for display. */
export function formatTs(ts?: Timestamp | string | null): string {
  if (!ts) return "—";
  let d: Date;
  if (typeof ts === "string") {
    d = new Date(ts);
  } else {
    const ms = Number(ts.seconds) * 1000 + Math.floor((ts.nanos || 0) / 1e6);
    d = new Date(ms);
  }
  if (Number.isNaN(d.getTime())) return "—";
  return d.toISOString().replace("T", " ").replace(/\.\d{3}Z$/, "Z");
}

export function formatBytes(n?: number | bigint | null): string {
  if (n === undefined || n === null) return "—";
  const v = typeof n === "bigint" ? Number(n) : n;
  if (!Number.isFinite(v)) return String(n);
  if (v < 1024) return `${v} B`;
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KB`;
  return `${(v / (1024 * 1024)).toFixed(1)} MB`;
}

export function formatCount(n?: number | bigint | null): string {
  if (n === undefined || n === null) return "0";
  const v = typeof n === "bigint" ? Number(n) : n;
  return new Intl.NumberFormat("en-US").format(v);
}

export function shortId(id?: string | null, keep = 8): string {
  if (!id) return "—";
  if (id.length <= keep + 4) return id;
  return `${id.slice(0, keep)}…`;
}

export function safeStringify(value: unknown, space = 2): string {
  return JSON.stringify(
    value,
    (_k, v) => {
      if (typeof v === "bigint") return v.toString();
      if (v instanceof Uint8Array) {
        return { __bytes: v.length, preview: bytesPreview(v) };
      }
      return v;
    },
    space,
  );
}

function bytesPreview(buf: Uint8Array, max = 32): string {
  const slice = buf.slice(0, max);
  const hex = Array.from(slice)
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  return buf.length > max ? `${hex}…` : hex;
}

/** Extract a plain JSON-friendly object from protobuf messages / structs. */
export function toPlain(value: unknown): unknown {
  if (value === null || value === undefined) return value;
  if (typeof value === "bigint") return value.toString();
  if (value instanceof Uint8Array) {
    return { __bytes: value.length, preview: bytesPreview(value) };
  }
  if (Array.isArray(value)) return value.map(toPlain);
  if (typeof value === "object") {
    // protobuf Timestamp-like
    const maybe = value as { seconds?: bigint | number; nanos?: number; $typeName?: string };
    if (maybe && "seconds" in maybe && "nanos" in maybe && !("$unknown" in (value as object) && Object.keys(value as object).length > 5)) {
      // Prefer structured if it looks like Timestamp and has few keys.
      const keys = Object.keys(value as object);
      if (keys.every((k) => ["seconds", "nanos", "$typeName", "$unknown"].includes(k))) {
        return formatTs(value as Timestamp);
      }
    }
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      if (k.startsWith("$")) continue;
      out[k] = toPlain(v);
    }
    return out;
  }
  return value;
}
