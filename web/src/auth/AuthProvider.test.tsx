import { StrictMode, useState } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "../test/setup";
import { AuthProvider, useAuth } from "./AuthProvider";

const adminSession = {
  user: {
    id: "user-admin",
    username: "admin",
    display_name: "Ada Lovelace",
    role: "admin" as const,
  },
  csrf_token: "csrf-session-token",
  expires_at: "2026-08-30T09:00:00Z",
};

const replacementSession = {
  user: {
    id: "user-replacement",
    username: "grace",
    display_name: "Grace Hopper",
    role: "admin" as const,
  },
  csrf_token: "csrf-replacement-token",
  expires_at: "2026-08-30T10:00:00Z",
};

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>((promiseResolve) => {
    resolve = promiseResolve;
  });
  return { promise, resolve };
}

function AuthProbe() {
  const { client, loading, login, logout, user } = useAuth();
  const [authOperationSettlements, setAuthOperationSettlements] = useState(0);
  const [requestState, setRequestState] = useState("idle");

  function recordAuthSettlement() {
    setAuthOperationSettlements((settlements) => settlements + 1);
  }

  return (
    <div>
      <output aria-label="Auth status">{loading ? "loading" : "ready"}</output>
      <output aria-label="Current user">
        {user?.display_name ?? "signed out"}
      </output>
      <button
        type="button"
        onClick={() =>
          void login("admin", "password")
            .catch(() => undefined)
            .finally(recordAuthSettlement)
        }
      >
        Log in probe
      </button>
      <button
        type="button"
        onClick={() =>
          void logout().catch(() => undefined).finally(recordAuthSettlement)
        }
      >
        Log out probe
      </button>
      <button
        type="button"
        onClick={() => {
          void client
            .get("/projects")
            .catch(() => undefined)
            .finally(() => setRequestState("finished"));
        }}
      >
        Load projects
      </button>
      <button
        type="button"
        onClick={() => {
          void client
            .put("/projects/shop/environments/prod/config", {})
            .catch(() => undefined)
            .finally(() => setRequestState("saved"));
        }}
      >
        Save project
      </button>
      <output aria-label="Auth operation settlements">
        {authOperationSettlements}
      </output>
      <output aria-label="Request status">{requestState}</output>
    </div>
  );
}

describe("AuthProvider", () => {
  it("bootstraps the current session once under StrictMode", async () => {
    let sessionRequests = 0;
    server.use(
      http.get("/api/v1/auth/session", () => {
        sessionRequests += 1;
        return HttpResponse.json(adminSession);
      }),
    );

    render(
      <StrictMode>
        <AuthProvider>
          <AuthProbe />
        </AuthProvider>
      </StrictMode>,
    );

    expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();
    expect(screen.getByLabelText("Auth status")).toHaveTextContent("ready");
    expect(sessionRequests).toBe(1);
  });

  it("logs in with credentials and retains the returned session in memory", async () => {
    let loginBody: unknown;
    server.use(
      http.get("/api/v1/auth/session", () =>
        HttpResponse.json(
          {
            error: {
              code: "invalid_session",
              message: "expired",
              request_id: "req_1",
              fields: {},
            },
          },
          { status: 401 },
        ),
      ),
      http.post("/api/v1/auth/login", async ({ request }) => {
        loginBody = await request.json();
        return HttpResponse.json(adminSession);
      }),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    await waitFor(() =>
      expect(screen.getByLabelText("Auth status")).toHaveTextContent("ready"),
    );
    expect(screen.getByLabelText("Current user")).toHaveTextContent("signed out");

    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));

    expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();
    expect(loginBody).toEqual({ username: "admin", password: "password" });
  });

  it("sends in-memory CSRF on logout and clears the session", async () => {
    let csrfHeader: string | null = null;
    server.use(
      http.get("/api/v1/auth/session", () => HttpResponse.json(adminSession)),
      http.post("/api/v1/auth/logout", ({ request }) => {
        csrfHeader = request.headers.get("X-CSRF-Token");
        return new HttpResponse(null, { status: 204 });
      }),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Log out probe" }));

    expect(await screen.findByText("signed out")).toBeInTheDocument();
    expect(csrfHeader).toBe("csrf-session-token");
  });

  it("clears auth and CSRF when logout fails without a 401", async () => {
    let mutationCSRF: string | null = "not-called";
    server.use(
      http.get("/api/v1/auth/session", () => HttpResponse.json(adminSession)),
      http.post("/api/v1/auth/logout", () =>
        HttpResponse.json(
          {
            error: {
              code: "service_unavailable",
              message: "unavailable",
              request_id: "req_logout_failure",
              fields: {},
            },
          },
          { status: 503 },
        ),
      ),
      http.put(
        "/api/v1/projects/shop/environments/prod/config",
        ({ request }) => {
          mutationCSRF = request.headers.get("X-CSRF-Token");
          return HttpResponse.json({ revision: 6 });
        },
      ),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Log out probe" }));
    expect(await screen.findByText("signed out")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Save project" }));
    await waitFor(() => expect(mutationCSRF).toBeNull());
  });

  it("clears auth and CSRF when a current child request receives 401", async () => {
    let mutationCSRF: string | null = "not-called";
    server.use(
      http.get("/api/v1/auth/session", () => HttpResponse.json(adminSession)),
      http.get("/api/v1/projects", () =>
        HttpResponse.json(
          {
            error: {
              code: "invalid_session",
              message: "expired",
              request_id: "req_2",
              fields: {},
            },
          },
          { status: 401 },
        ),
      ),
      http.put(
        "/api/v1/projects/shop/environments/prod/config",
        ({ request }) => {
          mutationCSRF = request.headers.get("X-CSRF-Token");
          return HttpResponse.json({ revision: 1 });
        },
      ),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Load projects" }));

    expect(await screen.findByText("signed out")).toBeInTheDocument();
    expect(screen.getByText("finished")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Save project" }));
    await waitFor(() => expect(mutationCSRF).toBeNull());
  });

  it("ignores a stale child 401 after logout and replacement login", async () => {
    const oldRequestStarted = createDeferred<void>();
    const releaseOldRequest = createDeferred<void>();
    let mutationCSRF: string | null = "not-called";
    server.use(
      http.get("/api/v1/auth/session", () => HttpResponse.json(adminSession)),
      http.get("/api/v1/projects", async () => {
        oldRequestStarted.resolve();
        await releaseOldRequest.promise;
        return HttpResponse.json(
          {
            error: {
              code: "invalid_session",
              message: "expired",
              request_id: "req_old",
              fields: {},
            },
          },
          { status: 401 },
        );
      }),
      http.post("/api/v1/auth/logout", () =>
        new HttpResponse(null, { status: 204 }),
      ),
      http.post("/api/v1/auth/login", () =>
        HttpResponse.json(replacementSession),
      ),
      http.put(
        "/api/v1/projects/shop/environments/prod/config",
        ({ request }) => {
          mutationCSRF = request.headers.get("X-CSRF-Token");
          return HttpResponse.json({ revision: 2 });
        },
      ),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Load projects" }));
    await oldRequestStarted.promise;
    await userEvent.click(screen.getByRole("button", { name: "Log out probe" }));
    expect(await screen.findByText("signed out")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    expect(await screen.findByText("Grace Hopper")).toBeInTheDocument();

    releaseOldRequest.resolve();
    expect(await screen.findByText("finished")).toBeInTheDocument();
    expect(screen.getByLabelText("Current user")).toHaveTextContent(
      "Grace Hopper",
    );

    await userEvent.click(screen.getByRole("button", { name: "Save project" }));
    await waitFor(() => expect(mutationCSRF).toBe("csrf-replacement-token"));
  });

  it("ignores stale bootstrap success after a newer login under StrictMode", async () => {
    const bootstrapStarted = createDeferred<void>();
    const releaseBootstrap = createDeferred<void>();
    server.use(
      http.get("/api/v1/auth/session", async () => {
        bootstrapStarted.resolve();
        await releaseBootstrap.promise;
        return HttpResponse.json(adminSession);
      }),
      http.post("/api/v1/auth/login", () =>
        HttpResponse.json(replacementSession),
      ),
    );

    render(
      <StrictMode>
        <AuthProvider>
          <AuthProbe />
        </AuthProvider>
      </StrictMode>,
    );
    await bootstrapStarted.promise;

    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    expect(await screen.findByText("Grace Hopper")).toBeInTheDocument();

    releaseBootstrap.resolve();
    await waitFor(() =>
      expect(screen.getByLabelText("Auth status")).toHaveTextContent("ready"),
    );
    expect(screen.getByLabelText("Current user")).toHaveTextContent(
      "Grace Hopper",
    );
  });

  it("lets a later login win when bootstrap succeeds first", async () => {
    const bootstrapStarted = createDeferred<void>();
    const releaseBootstrap = createDeferred<void>();
    const bootstrapHandlerReleased = createDeferred<void>();
    const loginStarted = createDeferred<void>();
    const releaseLogin = createDeferred<void>();
    let mutationCSRF: string | null = "not-called";
    server.use(
      http.get("/api/v1/auth/session", async () => {
        bootstrapStarted.resolve();
        await releaseBootstrap.promise;
        bootstrapHandlerReleased.resolve();
        return HttpResponse.json(adminSession);
      }),
      http.post("/api/v1/auth/login", async () => {
        loginStarted.resolve();
        await releaseLogin.promise;
        return HttpResponse.json(replacementSession);
      }),
      http.put(
        "/api/v1/projects/shop/environments/prod/config",
        ({ request }) => {
          mutationCSRF = request.headers.get("X-CSRF-Token");
          return HttpResponse.json({ revision: 3 });
        },
      ),
    );

    render(
      <StrictMode>
        <AuthProvider>
          <AuthProbe />
        </AuthProvider>
      </StrictMode>,
    );
    await bootstrapStarted.promise;

    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    await loginStarted.promise;
    await act(async () => {
      releaseBootstrap.resolve();
      await bootstrapHandlerReleased.promise;
    });

    expect(screen.getByLabelText("Current user")).toHaveTextContent("signed out");
    expect(screen.getByLabelText("Auth status")).toHaveTextContent("loading");

    releaseLogin.resolve();
    expect(await screen.findByText("Grace Hopper")).toBeInTheDocument();
    expect(screen.getByLabelText("Auth status")).toHaveTextContent("ready");

    await userEvent.click(screen.getByRole("button", { name: "Save project" }));
    await waitFor(() => expect(mutationCSRF).toBe("csrf-replacement-token"));
  });

  it("lets a later logout win when bootstrap succeeds first", async () => {
    const bootstrapStarted = createDeferred<void>();
    const releaseBootstrap = createDeferred<void>();
    const bootstrapHandlerReleased = createDeferred<void>();
    const logoutStarted = createDeferred<void>();
    const releaseLogout = createDeferred<void>();
    let mutationCSRF: string | null = "not-called";
    server.use(
      http.get("/api/v1/auth/session", async () => {
        bootstrapStarted.resolve();
        await releaseBootstrap.promise;
        bootstrapHandlerReleased.resolve();
        return HttpResponse.json(adminSession);
      }),
      http.post("/api/v1/auth/logout", async () => {
        logoutStarted.resolve();
        await releaseLogout.promise;
        return new HttpResponse(null, { status: 204 });
      }),
      http.put(
        "/api/v1/projects/shop/environments/prod/config",
        ({ request }) => {
          mutationCSRF = request.headers.get("X-CSRF-Token");
          return HttpResponse.json({ revision: 4 });
        },
      ),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    await bootstrapStarted.promise;

    await userEvent.click(screen.getByRole("button", { name: "Log out probe" }));
    await logoutStarted.promise;
    await act(async () => {
      releaseBootstrap.resolve();
      await bootstrapHandlerReleased.promise;
    });

    expect(screen.getByLabelText("Current user")).toHaveTextContent("signed out");
    expect(screen.getByLabelText("Auth status")).toHaveTextContent("loading");

    releaseLogout.resolve();
    await waitFor(() =>
      expect(screen.getByLabelText("Auth status")).toHaveTextContent("ready"),
    );
    expect(screen.getByLabelText("Current user")).toHaveTextContent("signed out");

    await userEvent.click(screen.getByRole("button", { name: "Save project" }));
    await waitFor(() => expect(mutationCSRF).toBeNull());
  });

  it("lets a pending login survive an older child 401", async () => {
    const oldRequestStarted = createDeferred<void>();
    const releaseOldRequest = createDeferred<void>();
    const loginStarted = createDeferred<void>();
    const releaseLogin = createDeferred<void>();
    let mutationCSRF: string | null = "not-called";
    server.use(
      http.get("/api/v1/auth/session", () => HttpResponse.json(adminSession)),
      http.get("/api/v1/projects", async () => {
        oldRequestStarted.resolve();
        await releaseOldRequest.promise;
        return HttpResponse.json(
          {
            error: {
              code: "invalid_session",
              message: "expired",
              request_id: "req_old_during_login",
              fields: {},
            },
          },
          { status: 401 },
        );
      }),
      http.post("/api/v1/auth/login", async () => {
        loginStarted.resolve();
        await releaseLogin.promise;
        return HttpResponse.json(replacementSession);
      }),
      http.put(
        "/api/v1/projects/shop/environments/prod/config",
        ({ request }) => {
          mutationCSRF = request.headers.get("X-CSRF-Token");
          return HttpResponse.json({ revision: 5 });
        },
      ),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Load projects" }));
    await oldRequestStarted.promise;
    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    await loginStarted.promise;

    releaseOldRequest.resolve();
    expect(await screen.findByText("finished")).toBeInTheDocument();
    expect(screen.getByLabelText("Current user")).toHaveTextContent(
      "Ada Lovelace",
    );

    releaseLogin.resolve();
    expect(await screen.findByText("Grace Hopper")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Save project" }));
    await waitFor(() => expect(mutationCSRF).toBe("csrf-replacement-token"));
  });

  it("lets the later of two pending logins win when the first succeeds first", async () => {
    const firstLoginStarted = createDeferred<void>();
    const releaseFirstLogin = createDeferred<void>();
    const secondLoginStarted = createDeferred<void>();
    const releaseSecondLogin = createDeferred<void>();
    let loginRequests = 0;
    server.use(
      http.get("/api/v1/auth/session", () =>
        HttpResponse.json(
          {
            error: {
              code: "invalid_session",
              message: "expired",
              request_id: "req_signed_out",
              fields: {},
            },
          },
          { status: 401 },
        ),
      ),
      http.post("/api/v1/auth/login", async () => {
        loginRequests += 1;
        if (loginRequests === 1) {
          firstLoginStarted.resolve();
          await releaseFirstLogin.promise;
          return HttpResponse.json(adminSession);
        }

        secondLoginStarted.resolve();
        await releaseSecondLogin.promise;
        return HttpResponse.json(replacementSession);
      }),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    await waitFor(() =>
      expect(screen.getByLabelText("Auth status")).toHaveTextContent("ready"),
    );

    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    await firstLoginStarted.promise;
    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    expect(loginRequests).toBe(1);

    releaseFirstLogin.resolve();
    await waitFor(() =>
      expect(
        screen.getByLabelText("Auth operation settlements"),
      ).toHaveTextContent("1"),
    );
    expect(screen.getByLabelText("Current user")).toHaveTextContent("signed out");

    await secondLoginStarted.promise;
    releaseSecondLogin.resolve();
    expect(await screen.findByText("Grace Hopper")).toBeInTheDocument();
    expect(loginRequests).toBe(2);
  });

  it("serializes logout before a later login", async () => {
    const logoutStarted = createDeferred<void>();
    const releaseLogout = createDeferred<void>();
    const loginStarted = createDeferred<void>();
    const releaseLogin = createDeferred<void>();
    let loginRequests = 0;
    server.use(
      http.get("/api/v1/auth/session", () => HttpResponse.json(adminSession)),
      http.post("/api/v1/auth/logout", async () => {
        logoutStarted.resolve();
        await releaseLogout.promise;
        return new HttpResponse(null, { status: 204 });
      }),
      http.post("/api/v1/auth/login", async () => {
        loginRequests += 1;
        loginStarted.resolve();
        await releaseLogin.promise;
        return HttpResponse.json(replacementSession);
      }),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Log out probe" }));
    await logoutStarted.promise;
    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    expect(loginRequests).toBe(0);

    releaseLogout.resolve();
    await loginStarted.promise;
    expect(screen.getByLabelText("Current user")).toHaveTextContent(
      "Ada Lovelace",
    );

    releaseLogin.resolve();
    expect(await screen.findByText("Grace Hopper")).toBeInTheDocument();
  });

  it("serializes login before a later logout with the login session CSRF", async () => {
    const loginStarted = createDeferred<void>();
    const releaseLogin = createDeferred<void>();
    const logoutStarted = createDeferred<void>();
    let logoutRequests = 0;
    let logoutCSRF: string | null = "not-called";
    server.use(
      http.get("/api/v1/auth/session", () => HttpResponse.json(adminSession)),
      http.post("/api/v1/auth/login", async () => {
        loginStarted.resolve();
        await releaseLogin.promise;
        return HttpResponse.json(replacementSession);
      }),
      http.post("/api/v1/auth/logout", ({ request }) => {
        logoutRequests += 1;
        logoutCSRF = request.headers.get("X-CSRF-Token");
        logoutStarted.resolve();
        return new HttpResponse(null, { status: 204 });
      }),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    await loginStarted.promise;
    await userEvent.click(screen.getByRole("button", { name: "Log out probe" }));
    expect(logoutRequests).toBe(0);

    releaseLogin.resolve();
    await logoutStarted.promise;
    expect(logoutCSRF).toBe("csrf-replacement-token");
    expect(await screen.findByText("signed out")).toBeInTheDocument();
    expect(screen.queryByText("Grace Hopper")).not.toBeInTheDocument();
  });

  it("reconciles a malformed login before releasing a queued logout", async () => {
    const loginStarted = createDeferred<void>();
    const releaseLogin = createDeferred<void>();
    let serverSession: typeof adminSession | typeof replacementSession | null =
      adminSession;
    let sessionRequests = 0;
    let logoutRequests = 0;
    let logoutCSRF: string | null = "not-called";
    server.use(
      http.get("/api/v1/auth/session", () => {
        sessionRequests += 1;
        if (serverSession === null) {
          return HttpResponse.json(
            {
              error: {
                code: "invalid_session",
                message: "signed out",
                request_id: "req_after_malformed_login_logout",
                fields: {},
              },
            },
            { status: 401 },
          );
        }
        return HttpResponse.json(serverSession);
      }),
      http.post("/api/v1/auth/login", async () => {
        loginStarted.resolve();
        await releaseLogin.promise;
        serverSession = replacementSession;
        return new HttpResponse("not-json", {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }),
      http.post("/api/v1/auth/logout", ({ request }) => {
        logoutRequests += 1;
        logoutCSRF = request.headers.get("X-CSRF-Token");
        if (logoutCSRF === replacementSession.csrf_token) {
          serverSession = null;
          return new HttpResponse(null, { status: 204 });
        }
        return HttpResponse.json(
          {
            error: {
              code: "invalid_csrf_token",
              message: "invalid CSRF token",
              request_id: "req_stale_csrf_after_malformed_login",
              fields: {},
            },
          },
          { status: 403 },
        );
      }),
    );

    const { unmount } = render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    await loginStarted.promise;
    await userEvent.click(screen.getByRole("button", { name: "Log out probe" }));
    expect(logoutRequests).toBe(0);

    releaseLogin.resolve();
    await waitFor(() =>
      expect(
        screen.getByLabelText("Auth operation settlements"),
      ).toHaveTextContent("2"),
    );

    expect(logoutCSRF).toBe("csrf-replacement-token");
    expect(screen.getByLabelText("Current user")).toHaveTextContent("signed out");
    expect(sessionRequests).toBe(2);

    unmount();
    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    await waitFor(() =>
      expect(screen.getByLabelText("Auth status")).toHaveTextContent("ready"),
    );
    expect(screen.getByLabelText("Current user")).toHaveTextContent("signed out");
    expect(sessionRequests).toBe(3);
  });

  it("reconciles the first login session when a later queued login is rejected", async () => {
    const firstLoginStarted = createDeferred<void>();
    const releaseFirstLogin = createDeferred<void>();
    const secondLoginStarted = createDeferred<void>();
    let loginRequests = 0;
    let sessionRequests = 0;
    let mutationCSRF: string | null = "not-called";
    server.use(
      http.get("/api/v1/auth/session", () => {
        sessionRequests += 1;
        if (sessionRequests === 1) {
          return HttpResponse.json(
            {
              error: {
                code: "invalid_session",
                message: "expired",
                request_id: "req_initially_signed_out",
                fields: {},
              },
            },
            { status: 401 },
          );
        }
        return HttpResponse.json(adminSession);
      }),
      http.post("/api/v1/auth/login", async () => {
        loginRequests += 1;
        if (loginRequests === 1) {
          firstLoginStarted.resolve();
          await releaseFirstLogin.promise;
          return HttpResponse.json(adminSession);
        }

        secondLoginStarted.resolve();
        return HttpResponse.json(
          {
            error: {
              code: "invalid_credentials",
              message: "rejected",
              request_id: "req_second_login",
              fields: {},
            },
          },
          { status: 401 },
        );
      }),
      http.put(
        "/api/v1/projects/shop/environments/prod/config",
        ({ request }) => {
          mutationCSRF = request.headers.get("X-CSRF-Token");
          return HttpResponse.json({ revision: 7 });
        },
      ),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    await waitFor(() =>
      expect(screen.getByLabelText("Auth status")).toHaveTextContent("ready"),
    );

    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    await firstLoginStarted.promise;
    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    releaseFirstLogin.resolve();
    await secondLoginStarted.promise;
    await waitFor(() =>
      expect(
        screen.getByLabelText("Auth operation settlements"),
      ).toHaveTextContent("2"),
    );

    expect(screen.getByLabelText("Current user")).toHaveTextContent(
      "Ada Lovelace",
    );
    expect(sessionRequests).toBe(2);

    await userEvent.click(screen.getByRole("button", { name: "Save project" }));
    await waitFor(() => expect(mutationCSRF).toBe("csrf-session-token"));
  });

  it("reconciles signed-out state when login fails after a queued logout", async () => {
    const logoutStarted = createDeferred<void>();
    const releaseLogout = createDeferred<void>();
    const loginStarted = createDeferred<void>();
    let sessionRequests = 0;
    let mutationCSRF: string | null = "not-called";
    server.use(
      http.get("/api/v1/auth/session", () => {
        sessionRequests += 1;
        if (sessionRequests === 1) {
          return HttpResponse.json(adminSession);
        }
        return HttpResponse.json(
          {
            error: {
              code: "invalid_session",
              message: "signed out",
              request_id: "req_after_logout",
              fields: {},
            },
          },
          { status: 401 },
        );
      }),
      http.post("/api/v1/auth/logout", async () => {
        logoutStarted.resolve();
        await releaseLogout.promise;
        return new HttpResponse(null, { status: 204 });
      }),
      http.post("/api/v1/auth/login", () => {
        loginStarted.resolve();
        return HttpResponse.json(
          {
            error: {
              code: "service_unavailable",
              message: "unavailable",
              request_id: "req_login_after_logout",
              fields: {},
            },
          },
          { status: 503 },
        );
      }),
      http.put(
        "/api/v1/projects/shop/environments/prod/config",
        ({ request }) => {
          mutationCSRF = request.headers.get("X-CSRF-Token");
          return HttpResponse.json({ revision: 8 });
        },
      ),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Log out probe" }));
    await logoutStarted.promise;
    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    releaseLogout.resolve();
    await loginStarted.promise;
    await waitFor(() =>
      expect(
        screen.getByLabelText("Auth operation settlements"),
      ).toHaveTextContent("2"),
    );

    expect(screen.getByLabelText("Current user")).toHaveTextContent("signed out");
    expect(sessionRequests).toBe(2);

    await userEvent.click(screen.getByRole("button", { name: "Save project" }));
    await waitFor(() => expect(mutationCSRF).toBeNull());
  });

  it("ignores a child 401 started while a newer login is pending", async () => {
    const loginStarted = createDeferred<void>();
    const releaseLogin = createDeferred<void>();
    const childRequestStarted = createDeferred<void>();
    const releaseChildRequest = createDeferred<void>();
    let mutationCSRF: string | null = "not-called";
    server.use(
      http.get("/api/v1/auth/session", () => HttpResponse.json(adminSession)),
      http.post("/api/v1/auth/login", async () => {
        loginStarted.resolve();
        await releaseLogin.promise;
        return HttpResponse.json(replacementSession);
      }),
      http.get("/api/v1/projects", async () => {
        childRequestStarted.resolve();
        await releaseChildRequest.promise;
        return HttpResponse.json(
          {
            error: {
              code: "invalid_session",
              message: "expired",
              request_id: "req_during_login",
              fields: {},
            },
          },
          { status: 401 },
        );
      }),
      http.put(
        "/api/v1/projects/shop/environments/prod/config",
        ({ request }) => {
          mutationCSRF = request.headers.get("X-CSRF-Token");
          return HttpResponse.json({ revision: 9 });
        },
      ),
    );

    render(
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>,
    );
    expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Log in probe" }));
    await loginStarted.promise;
    await userEvent.click(screen.getByRole("button", { name: "Load projects" }));
    await childRequestStarted.promise;
    releaseChildRequest.resolve();
    expect(await screen.findByText("finished")).toBeInTheDocument();
    expect(screen.getByLabelText("Current user")).toHaveTextContent(
      "Ada Lovelace",
    );

    releaseLogin.resolve();
    expect(await screen.findByText("Grace Hopper")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Save project" }));
    await waitFor(() => expect(mutationCSRF).toBe("csrf-replacement-token"));
  });
});
