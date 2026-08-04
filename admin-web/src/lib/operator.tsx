/**
 * Operator identity (role/permissions) loaded via WhoAmI after login.
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useAuth } from "./auth";
import { useAdminClient } from "./client";

export type OperatorInfo = {
  id: string;
  name: string;
  role: string;
  permissions: string[];
  revealEnabled: boolean;
};

type OperatorState = {
  operator: OperatorInfo | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  can: (command: string) => boolean;
};

const OperatorContext = createContext<OperatorState | null>(null);

export function OperatorProvider({ children }: { children: ReactNode }) {
  const { token, isAuthenticated } = useAuth();
  const client = useAdminClient();
  const [operator, setOperator] = useState<OperatorInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!token) {
      setOperator(null);
      return;
    }
    setLoading(true);
    try {
      const res = await client.whoAmI({});
      const op = res.operator;
      setOperator(
        op
          ? {
              id: op.id,
              name: op.name,
              role: op.role,
              permissions: op.permissions ?? [],
              revealEnabled: Boolean(op.revealEnabled),
            }
          : null,
      );
      setError(null);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
      setOperator(null);
    } finally {
      setLoading(false);
    }
  }, [client, token]);

  useEffect(() => {
    if (isAuthenticated) void refresh();
    else setOperator(null);
  }, [isAuthenticated, refresh]);

  const value = useMemo<OperatorState>(
    () => ({
      operator,
      loading,
      error,
      refresh,
      can: (command: string) => Boolean(operator?.permissions.includes(command)),
    }),
    [operator, loading, error, refresh],
  );

  return <OperatorContext.Provider value={value}>{children}</OperatorContext.Provider>;
}

export function useOperator(): OperatorState {
  const ctx = useContext(OperatorContext);
  if (!ctx) throw new Error("useOperator requires OperatorProvider");
  return ctx;
}
