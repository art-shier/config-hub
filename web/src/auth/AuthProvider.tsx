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
import { APIClient, APIError } from "../api/client";
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
  epoch: number;
  promise: Promise<Session>;
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const csrfTokenRef = useRef<string | null>(null);
  // Auth mutations run FIFO. The latest operation epoch alone owns visible UI,
  // request generations gate ordinary 401 cleanup, and stale reconciliation may
  // update only internal CSRF state before handing the queue to its next mutation.
  const operationEpochRef = useRef(0);
  const requestGenerationRef = useRef(0);
  const authTransitionPendingRef = useRef(false);
  const loadingOwnerRef = useRef<number | null>(0);
  const mountedRef = useRef(false);
  const bootstrapRequestRef = useRef<BootstrapRequest | null>(null);
  const authMutationQueueRef = useRef<Promise<void>>(Promise.resolve());

  const beginAuthOperation = useCallback(() => {
    operationEpochRef.current += 1;
    requestGenerationRef.current += 1;
    authTransitionPendingRef.current = true;
    const epoch = operationEpochRef.current;
    if (loadingOwnerRef.current !== null) {
      loadingOwnerRef.current = epoch;
    }
    return epoch;
  }, []);

  const clearAuthForOperation = useCallback((expectedEpoch: number) => {
    if (expectedEpoch !== operationEpochRef.current) {
      return;
    }

    authTransitionPendingRef.current = false;
    requestGenerationRef.current += 1;
    csrfTokenRef.current = null;
    if (mountedRef.current) {
      setUser(null);
    }
  }, []);

  const clearAuthForRequest = useCallback((expectedGeneration: number) => {
    if (
      authTransitionPendingRef.current ||
      expectedGeneration !== requestGenerationRef.current
    ) {
      return;
    }

    requestGenerationRef.current += 1;
    csrfTokenRef.current = null;
    if (mountedRef.current) {
      setUser(null);
    }
  }, []);

  const preserveAuthForOperation = useCallback((expectedEpoch: number) => {
    if (expectedEpoch !== operationEpochRef.current) {
      return;
    }

    authTransitionPendingRef.current = false;
    requestGenerationRef.current += 1;
  }, []);

  const finishLoading = useCallback((expectedEpoch: number) => {
    if (loadingOwnerRef.current !== expectedEpoch) {
      return;
    }

    loadingOwnerRef.current = null;
    if (mountedRef.current) {
      setLoading(false);
    }
  }, []);

  const enqueueAuthMutation = useCallback(
    <T,>(operation: () => Promise<T>): Promise<T> => {
      const result = authMutationQueueRef.current.then(operation);
      authMutationQueueRef.current = result.then(
        () => undefined,
        () => undefined,
      );
      return result;
    },
    [],
  );

  const clientRef = useRef<APIClient | null>(null);
  if (clientRef.current === null) {
    clientRef.current = new APIClient(
      () => csrfTokenRef.current,
      clearAuthForRequest,
      () => requestGenerationRef.current,
    );
  }
  const client = clientRef.current;

  const applySession = useCallback(
    (session: Session, expectedEpoch: number) => {
      if (
        !mountedRef.current ||
        expectedEpoch !== operationEpochRef.current
      ) {
        return;
      }

      authTransitionPendingRef.current = false;
      requestGenerationRef.current += 1;
      csrfTokenRef.current = session.csrf_token;
      setUser(session.user);
    },
    [],
  );

  const reconcileSession = useCallback(
    async (expectedEpoch: number) => {
      const reconciliationClient = new APIClient(() => csrfTokenRef.current);
      try {
        const session = await reconciliationClient.get<Session>("/auth/session");
        csrfTokenRef.current = session.csrf_token;
        applySession(session, expectedEpoch);
      } catch {
        csrfTokenRef.current = null;
        clearAuthForOperation(expectedEpoch);
      }
    },
    [applySession, clearAuthForOperation],
  );

  const reconcileLogoutSession = useCallback(
    async (expectedEpoch: number) => {
      const reconciliationClient = new APIClient(() => csrfTokenRef.current);
      try {
        const session = await reconciliationClient.get<Session>("/auth/session");
        csrfTokenRef.current = session.csrf_token;
        applySession(session, expectedEpoch);
        return false;
      } catch (error) {
        if (isInvalidSession(error)) {
          csrfTokenRef.current = null;
          clearAuthForOperation(expectedEpoch);
          return true;
        }
        preserveAuthForOperation(expectedEpoch);
        return false;
      }
    },
    [applySession, clearAuthForOperation, preserveAuthForOperation],
  );

  useEffect(() => {
    mountedRef.current = true;
    let active = true;

    if (bootstrapRequestRef.current === null) {
      const epoch = beginAuthOperation();
      bootstrapRequestRef.current = {
        epoch,
        promise: client.get<Session>("/auth/session"),
      };
    }
    const bootstrapRequest = bootstrapRequestRef.current;
    void bootstrapRequest.promise
      .then((session) => {
        if (active) {
          applySession(session, bootstrapRequest.epoch);
        }
      })
      .catch(() => {
        if (active) {
          clearAuthForOperation(bootstrapRequest.epoch);
        }
      })
      .finally(() => {
        if (active) {
          finishLoading(bootstrapRequest.epoch);
        }
      });

    return () => {
      active = false;
      mountedRef.current = false;
    };
  }, [
    applySession,
    beginAuthOperation,
    clearAuthForOperation,
    client,
    finishLoading,
  ]);

  const login = useCallback(
    async (username: string, password: string) => {
      const epoch = beginAuthOperation();
      try {
        await enqueueAuthMutation(async () => {
          const operationClient = new APIClient(() => csrfTokenRef.current);
          try {
            const session = await operationClient.post<Session>("/auth/login", {
              username,
              password,
            });
            csrfTokenRef.current = session.csrf_token;
            applySession(session, epoch);
          } catch (error) {
            await reconcileSession(epoch);
            throw error;
          }
        });
      } finally {
        finishLoading(epoch);
      }
    },
    [
      applySession,
      beginAuthOperation,
      enqueueAuthMutation,
      finishLoading,
      reconcileSession,
    ],
  );

  const logout = useCallback(async () => {
    const epoch = beginAuthOperation();
    try {
      await enqueueAuthMutation(async () => {
        const operationClient = new APIClient(() => csrfTokenRef.current);
        try {
          await operationClient.postNoContent("/auth/logout", {});
        } catch (error) {
          if (isInvalidSession(error)) {
            csrfTokenRef.current = null;
            clearAuthForOperation(epoch);
            return;
          }
          if (await reconcileLogoutSession(epoch)) {
            return;
          }
          throw error;
        }
        csrfTokenRef.current = null;
        clearAuthForOperation(epoch);
      });
    } finally {
      finishLoading(epoch);
    }
  }, [
    beginAuthOperation,
    clearAuthForOperation,
    enqueueAuthMutation,
    finishLoading,
    reconcileLogoutSession,
  ]);

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

function isInvalidSession(error: unknown): boolean {
  return (
    error instanceof APIError &&
    error.status === 401 &&
    error.code === "invalid_session"
  );
}
