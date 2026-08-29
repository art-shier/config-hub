import { StrictMode, useState } from "react";
import { render, screen, waitFor } from "@testing-library/react";
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
  const [requestState, setRequestState] = useState("idle");

  return (
    <div>
      <output aria-label="Auth status">{loading ? "loading" : "ready"}</output>
      <output aria-label="Current user">
        {user?.display_name ?? "signed out"}
      </output>
      <button
        type="button"
        onClick={() => void login("admin", "password")}
      >
        Log in probe
      </button>
      <button type="button" onClick={() => void logout()}>
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
});
