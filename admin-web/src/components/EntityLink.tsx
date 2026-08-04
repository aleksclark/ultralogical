import { Link } from "react-router-dom";
import { cn } from "@/lib/cn";
import { shortId } from "@/lib/format";

export type EntityKind =
  | "tenant"
  | "session"
  | "run"
  | "resource"
  | "provider"
  | "job"
  | "event"
  | "api-key"
  | "credential"
  | "automation"
  | "wait"
  | "memory";

function pathFor(kind: EntityKind, id: string, extra?: Record<string, string>): string {
  switch (kind) {
    case "tenant":
      return `/tenants/${id}`;
    case "session":
      return `/sessions/${id}`;
    case "run":
      return `/runs/${id}`;
    case "resource":
      return `/resources/${id}`;
    case "provider":
      return `/providers/${id}`;
    case "job":
      return `/jobs/${id}`;
    case "event":
      return extra?.sessionId
        ? `/events?detail=${encodeURIComponent(`${extra.sessionId}:${id}`)}&f=${encodeURIComponent(`session_id:eq:${extra.sessionId}`)}`
        : `/events?detail=${encodeURIComponent(id)}`;
    case "api-key":
      return `/api-keys?detail=${encodeURIComponent(id)}`;
    case "credential":
      return `/credentials?detail=${encodeURIComponent(id)}`;
    case "automation":
      return `/automation?detail=${encodeURIComponent(id)}`;
    case "wait":
      return `/waits?detail=${encodeURIComponent(id)}`;
    case "memory":
      return extra?.sessionId
        ? `/memory?detail=${encodeURIComponent(`${extra.sessionId}:${id}`)}&f=${encodeURIComponent(`session_id:eq:${extra.sessionId}`)}`
        : `/memory?detail=${encodeURIComponent(id)}`;
    default:
      return "/";
  }
}

export function EntityLink({
  kind,
  id,
  label,
  className,
  title,
  extra,
  mono = true,
}: {
  kind: EntityKind;
  id?: string | null;
  label?: string;
  className?: string;
  title?: string;
  extra?: Record<string, string>;
  mono?: boolean;
}) {
  if (!id) return <span className="text-muted-foreground">—</span>;
  const text = label ?? shortId(id);
  return (
    <Link
      to={pathFor(kind, id, extra)}
      className={cn(
        "text-primary hover:underline",
        mono && "font-mono text-[12px]",
        className,
      )}
      title={title ?? id}
      data-entity-kind={kind}
      data-entity-id={id}
    >
      {text}
    </Link>
  );
}

/** Build a list route deep link with a pre-applied filter. */
export function listFilterHref(
  path: string,
  filters: Array<{ field: string; op?: string; value: string }>,
  extra?: Record<string, string>,
): string {
  const sp = new URLSearchParams(extra);
  for (const f of filters) {
    sp.append("f", `${f.field}:${f.op ?? "eq"}:${f.value}`);
  }
  const q = sp.toString();
  return q ? `${path}?${q}` : path;
}
