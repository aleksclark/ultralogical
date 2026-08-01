import type { Org } from "@client/gen/ultra/v1/org_pb";
import type { Session } from "@client/gen/ultra/v1/session_pb";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { cn } from "@/lib/utils";

export type ConnectionState = "connecting" | "live" | "offline";

export function SessionSidebar({
  orgs,
  org,
  onSelectOrg,
  sessions,
  session,
  onSelectSession,
  title,
  onTitleChange,
  onCreateSession,
  connection,
  token,
  onTokenChange,
  onToggleSettings,
  endpoint,
  altEndpoint,
  onSwitchEndpoint,
}: {
  orgs: Org[];
  org?: Org;
  onSelectOrg: (id: string) => void;
  sessions: Session[];
  session?: Session;
  onSelectSession: (session: Session) => void;
  title: string;
  onTitleChange: (value: string) => void;
  onCreateSession: () => Promise<void>;
  connection: ConnectionState;
  token: string;
  onTokenChange: (value: string) => void;
  onToggleSettings: () => void;
  /** The replica this client is currently talking to. */
  endpoint: string;
  /** An alternate replica, when the deployment has more than one. */
  altEndpoint: string;
  onSwitchEndpoint: () => void;
}) {
  return (
    <aside className="flex w-72 flex-col gap-4 border-r border-zinc-800 p-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold tracking-tight">Ultralogical</h1>
        <Badge
          data-testid="connection-state"
          data-connection={connection}
          variant={connection === "live" ? "success" : connection === "connecting" ? "pending" : "destructive"}
        >
          {connection}
        </Badge>
      </div>
      <Select aria-label="Organization" value={org?.id ?? ""} onChange={(e) => onSelectOrg(e.target.value)}>
        {orgs.map((o) => (
          <option key={o.id} value={o.id}>
            {o.name}
          </option>
        ))}
      </Select>
      <div className="flex gap-2">
        <Input aria-label="New session title" value={title} onChange={(e) => onTitleChange(e.target.value)} />
        <Button size="icon" onClick={onCreateSession} aria-label="Create session">
          +
        </Button>
      </div>
      <nav data-testid="session-list" className="flex flex-col gap-1 overflow-auto">
        {sessions.map((s) => (
          <Button
            key={s.id}
            variant="ghost"
            onClick={() => onSelectSession(s)}
            className={cn("justify-start", session?.id === s.id && "bg-zinc-800 text-zinc-100")}
          >
            {s.title || "Untitled"}
          </Button>
        ))}
      </nav>
      {altEndpoint !== "" && (
        <div className="flex flex-col gap-1" data-testid="replica-switch" data-endpoint={endpoint}>
          <span className="text-xs text-zinc-500">
            replica: {endpoint.replace(/^https?:\/\//, "")}
          </span>
          <Button variant="outline" size="sm" onClick={onSwitchEndpoint} aria-label="Reconnect through another replica">
            Switch replica
          </Button>
        </div>
      )}
      <div className="mt-auto flex items-center gap-2">
        <Button variant="ghost" size="sm" onClick={onToggleSettings}>
          Settings
        </Button>
        <Input
          aria-label="API token"
          value={token}
          onChange={(e) => onTokenChange(e.target.value)}
          className="h-8 w-28 text-xs"
        />
      </div>
    </aside>
  );
}
