/**
 * Operator auth for the private admin SPA.
 *
 * Token storage policy (documented):
 * - Prefer in-memory token (lost on full reload).
 * - Optionally mirror into sessionStorage under a non-secret-looking key so a
 *   tab refresh keeps the operator session for the browser tab lifetime.
 * - Never write the operator token into localStorage, URL query, or cookies.
 * - Never accept/store tenant API keys (uck_…) as operator credentials.
 */
import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react";

const SESSION_KEY = "uc_admin_op_sess";

export type AuthState = {
  token: string | null;
  persistSession: boolean;
  setToken: (token: string, opts?: { persistSession?: boolean }) => void;
  clear: () => void;
  isAuthenticated: boolean;
};

const AuthContext = createContext<AuthState | null>(null);

function readSession(): string | null {
  try {
    return sessionStorage.getItem(SESSION_KEY);
  } catch {
    return null;
  }
}

function writeSession(token: string | null) {
  try {
    if (token) sessionStorage.setItem(SESSION_KEY, token);
    else sessionStorage.removeItem(SESSION_KEY);
  } catch {
    /* private mode */
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setTokenState] = useState<string | null>(() => readSession());
  const [persistSession, setPersistSession] = useState(true);

  const setToken = useCallback((next: string, opts?: { persistSession?: boolean }) => {
    const trimmed = next.trim();
    if (!trimmed) return;
    // Guard: tenant-shaped keys must never be used as operator auth.
    if (/^uck_/i.test(trimmed)) {
      throw new Error("Tenant API keys cannot authenticate the admin SPA. Use CORE_ADMIN_TOKEN.");
    }
    const persist = opts?.persistSession ?? true;
    setPersistSession(persist);
    setTokenState(trimmed);
    if (persist) writeSession(trimmed);
    else writeSession(null);
  }, []);

  const clear = useCallback(() => {
    setTokenState(null);
    writeSession(null);
  }, []);

  const value = useMemo<AuthState>(
    () => ({
      token,
      persistSession,
      setToken,
      clear,
      isAuthenticated: Boolean(token),
    }),
    [token, persistSession, setToken, clear],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth requires AuthProvider");
  return ctx;
}
