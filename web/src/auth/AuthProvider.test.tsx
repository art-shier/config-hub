import { StrictMode, useState } from "react";
import { render, screen } from "@testing-library/react";
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

function AuthProbe() {
  const { client, loading, login, logout, user } = useAuth();
  const [requestState, setRequestState] = useState("idle");

  return (
    <div>
      <output>{loading ? "loading" : (user?.display_name ?? "signed out")}</output>
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
      <output>{requestState}</output>
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
    expect(await screen.findByText("signed out")).toBeInTheDocument();

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

  it("clears auth when an authenticated child request receives 401", async () => {
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
  });
});
