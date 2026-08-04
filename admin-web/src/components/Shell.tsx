import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useAuth } from "@/lib/auth";
import { useOperator } from "@/lib/operator";
import { cn } from "@/lib/cn";
import { Button } from "./ui";

const NAV: { to: string; label: string; end?: boolean }[] = [
  { to: "/", label: "Overview", end: true },
  { to: "/tenants", label: "Tenants" },
  { to: "/sessions", label: "Sessions" },
  { to: "/events", label: "Events" },
  { to: "/runs", label: "Runs" },
  { to: "/resources", label: "Resources" },
  { to: "/providers", label: "Providers" },
  { to: "/jobs", label: "Jobs" },
  { to: "/automation", label: "Automation" },
  { to: "/memory", label: "Memory" },
  { to: "/waits", label: "Waits" },
  { to: "/credentials", label: "Credentials" },
  { to: "/api-keys", label: "API keys" },
  { to: "/security", label: "Security" },
  { to: "/audit", label: "Audit" },
  { to: "/internals", label: "Internals" },
];

const BUILD_SHA =
  (import.meta.env.VITE_BUILD_SHA as string | undefined)?.slice(0, 8) || "dev";

export function Shell() {
  const { clear } = useAuth();
  const { operator } = useOperator();
  const navigate = useNavigate();

  return (
    <div className="admin-shell-bg flex h-full min-h-0">
      <aside className="flex w-56 shrink-0 flex-col border-r bg-card/40">
        <div className="border-b px-4 py-4">
          <div className="text-xs font-semibold uppercase tracking-[0.18em] text-primary">
            ultracore
          </div>
          <div className="text-sm font-semibold">Admin</div>
          {operator && (
            <div className="mt-1 text-[10px] text-muted-foreground" data-testid="operator-role">
              {operator.name} · {operator.role}
            </div>
          )}
          <div className="mt-1 font-mono text-[10px] text-muted-foreground">build {BUILD_SHA}</div>
        </div>
        <nav className="flex-1 space-y-0.5 overflow-auto p-2" aria-label="Primary">
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                cn(
                  "block rounded-md px-3 py-2 text-sm transition-colors",
                  isActive
                    ? "bg-primary/15 font-medium text-primary"
                    : "text-muted-foreground hover:bg-accent hover:text-foreground",
                )
              }
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="border-t p-3">
          <Button
            variant="outline"
            size="sm"
            className="w-full"
            onClick={() => {
              clear();
              navigate("/login");
            }}
          >
            Sign out
          </Button>
        </div>
      </aside>
      <main className="flex min-w-0 flex-1 flex-col overflow-auto p-5">
        <Outlet />
      </main>
    </div>
  );
}
