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

interface BootstrapRequest {
  generation: number;
  promise: Promise<Session>;
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const csrfTokenRef = useRef<string | null>(null);
  const authGenerationRef = useRef(0);
  const mountedRef = useRef(false);
  const bootstrapRequestRef = useRef<BootstrapRequest | null>(null);

  const clearAuth = useCallback((expectedGeneration?: number) => {
    const generation = expectedGeneration ?? authGenerationRef.current;
    if (generation !== authGenerationRef.current) {
      return;
    }

    authGenerationRef.current += 1;
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
      () => authGenerationRef.current,
    );
  }
  const client = clientRef.current;

  const applySession = useCallback(
    (session: Session, expectedGeneration: number) => {
      if (
        !mountedRef.current ||
        expectedGeneration !== authGenerationRef.current
      ) {
        return;
      }

      authGenerationRef.current += 1;
      csrfTokenRef.current = session.csrf_token;
      setUser(session.user);
    },
    [],
  );

  useEffect(() => {
    mountedRef.current = true;
    let active = true;

    if (bootstrapRequestRef.current === null) {
      bootstrapRequestRef.current = {
        generation: authGenerationRef.current,
        promise: client.get<Session>("/auth/session"),
      };
    }
    const bootstrapRequest = bootstrapRequestRef.current;
    void bootstrapRequest.promise
      .then((session) => {
        if (active) {
          applySession(session, bootstrapRequest.generation);
        }
      })
      .catch(() => {
        if (active) {
          clearAuth(bootstrapRequest.generation);
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
      const generation = authGenerationRef.current;
      const session = await client.post<Session>("/auth/login", {
        username,
        password,
      });
      applySession(session, generation);
    },
    [applySession, client],
  );

  const logout = useCallback(async () => {
    const generation = authGenerationRef.current;
    try {
      await client.post<void>("/auth/logout", {});
    } finally {
      clearAuth(generation);
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
