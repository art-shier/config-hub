import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { APIClient } from "../api/client";
import type { Session, User } from "../api/types";

interface AuthContextValue {
  user: User | null;
  loading: boolean;
  client: APIClient;
  login(username: string, password: string): Promise<void>;
  logout(): Promise<void>;
}

const AuthContext = createContext<AuthContextValue | undefined>(undefined);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const csrfTokenRef = useRef<string | null>(null);
  const mountedRef = useRef(false);
  const bootstrapRequestRef = useRef<Promise<Session> | null>(null);

  const clearAuth = useCallback(() => {
    csrfTokenRef.current = null;
    if (mountedRef.current) {
      setUser(null);
    }
  }, []);

  const clientRef = useRef<APIClient | null>(null);
  if (clientRef.current === null) {
    clientRef.current = new APIClient(
      () => csrfTokenRef.current,
      clearAuth,
    );
  }
  const client = clientRef.current;

  const applySession = useCallback((session: Session) => {
    csrfTokenRef.current = session.csrf_token;
    if (mountedRef.current) {
      setUser(session.user);
    }
  }, []);

  useEffect(() => {
    mountedRef.current = true;
    let active = true;

    bootstrapRequestRef.current ??=
      client.get<Session>("/auth/session");
    void bootstrapRequestRef.current
      .then((session) => {
        if (active) {
          applySession(session);
        }
      })
      .catch(() => {
        if (active) {
          clearAuth();
        }
      })
      .finally(() => {
        if (active && mountedRef.current) {
          setLoading(false);
        }
      });

    return () => {
      active = false;
      mountedRef.current = false;
    };
  }, [applySession, clearAuth, client]);

  const login = useCallback(
    async (username: string, password: string) => {
      const session = await client.post<Session>("/auth/login", {
        username,
        password,
      });
      applySession(session);
    },
    [applySession, client],
  );

  const logout = useCallback(async () => {
    try {
      await client.post<void>("/auth/logout", {});
    } finally {
      clearAuth();
    }
  }, [clearAuth, client]);

  const value = useMemo<AuthContextValue>(
    () => ({ client, loading, login, logout, user }),
    [client, loading, login, logout, user],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (context === undefined) {
    throw new Error("useAuth must be used within AuthProvider");
  }
  return context;
}
