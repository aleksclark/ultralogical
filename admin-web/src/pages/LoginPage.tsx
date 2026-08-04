import { useState, type FormEvent } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { useAuth } from "@/lib/auth";
import { Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Input, Label } from "@/components/ui";

export function LoginPage() {
  const { isAuthenticated, setToken } = useAuth();
  const navigate = useNavigate();
  const [token, setLocal] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [persist, setPersist] = useState(true);

  if (isAuthenticated) return <Navigate to="/" replace />;

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      setToken(token, { persistSession: persist });
      navigate("/", { replace: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <div className="admin-shell-bg flex min-h-full items-center justify-center p-6">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle className="text-lg">ultracore admin</CardTitle>
          <CardDescription>
            Private operator console. Authenticate with <code className="font-mono">CORE_ADMIN_TOKEN</code>.
            Tenant API keys are rejected.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form className="space-y-4" onSubmit={onSubmit} data-testid="login-form">
            <div className="space-y-1.5">
              <Label htmlFor="token">Operator bearer token</Label>
              <Input
                id="token"
                data-testid="login-token"
                type="password"
                autoComplete="off"
                value={token}
                onChange={(e) => setLocal(e.target.value)}
                placeholder="CORE_ADMIN_TOKEN"
                required
              />
            </div>
            <label className="flex items-center gap-2 text-xs text-muted-foreground">
              <input
                type="checkbox"
                checked={persist}
                onChange={(e) => setPersist(e.target.checked)}
              />
              Keep session for this browser tab (sessionStorage only — never localStorage)
            </label>
            {error ? (
              <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                {error}
              </div>
            ) : null}
            <Button type="submit" className="w-full" data-testid="login-submit">
              Sign in
            </Button>
          </form>
          <p className="mt-4 text-[11px] leading-relaxed text-muted-foreground">
            Token is held in memory and optionally sessionStorage for the tab lifetime. It is never
            written to the URL, localStorage, or analytics. SPA talks only to admin.v1.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
