import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { delay, http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { App } from "../app/App";
import { server } from "../test/setup";

const adminSession = {
  user: {
    id: "user-admin",
    username: "admin",
    display_name: "Ada Lovelace",
    role: "admin" as const,
  },
  csrf_token: "csrf-login-token",
  expires_at: "2026-08-30T09:00:00Z",
};

const memberSession = {
  user: {
    id: "user-member",
    username: "lee",
    display_name: "Lee Operator",
    role: "member" as const,
  },
  csrf_token: "csrf-member-token",
  expires_at: "2026-08-30T09:00:00Z",
};

function renderAppAt(path: string) {
  window.history.pushState({}, "", path);
  return render(<App />);
}

function useSignedOutSession() {
  server.use(
    http.get("/api/v1/auth/session", () =>
      HttpResponse.json(
        {
          error: {
            code: "invalid_session",
            message: "expired",
            request_id: "req_session",
            fields: {},
          },
        },
        { status: 401 },
      ),
    ),
  );
}

async function signIn(username = "admin", password = "password") {
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("Username"), username);
  await user.type(screen.getByLabelText("Password"), password);
  const submit = screen.getByRole("button", { name: "Sign in" });
  await waitFor(() => expect(submit).toBeEnabled());
  await user.click(submit);
}

describe("authentication routes", () => {
  it("logs in and redirects to projects", async () => {
    useSignedOutSession();
    server.use(
      http.post("/api/v1/auth/login", () => HttpResponse.json(adminSession)),
    );

    renderAppAt("/login");
    await signIn();

    expect(
      await screen.findByRole("heading", { name: "Projects" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Skip to content" }),
    ).toHaveAttribute("href", "#main-content");
  });

  it("shows a credential-safe error after a rejected login", async () => {
    useSignedOutSession();
    server.use(
      http.post("/api/v1/auth/login", () =>
        HttpResponse.json(
          {
            error: {
              code: "invalid_credentials",
              message: "account-specific server detail",
              request_id: "req_login",
              fields: { username: "does not exist" },
            },
          },
          { status: 401 },
        ),
      ),
    );

    renderAppAt("/login");
    await signIn("unknown", "wrong-password");

    const error = await screen.findByRole("alert");
    expect(error).toHaveTextContent("Username or password wasn’t recognized.");
    expect(error).not.toHaveTextContent("account-specific");
    expect(error).not.toHaveTextContent("does not exist");
    expect(screen.getByLabelText("Password")).toHaveValue("");
  });

  it("prevents duplicate sign-in submissions while loading", async () => {
    useSignedOutSession();
    let loginRequests = 0;
    server.use(
      http.post("/api/v1/auth/login", async () => {
        loginRequests += 1;
        await delay(80);
        return HttpResponse.json(adminSession);
      }),
    );

    renderAppAt("/login");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Username"), "admin");
    await user.type(screen.getByLabelText("Password"), "password");
    const submit = screen.getByRole("button", { name: "Sign in" });
    await waitFor(() => expect(submit).toBeEnabled());

    await user.click(submit);
    expect(screen.getByRole("button", { name: "Signing in…" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Signing in…" }));

    expect(
      await screen.findByRole("heading", { name: "Projects" }),
    ).toBeInTheDocument();
    expect(loginRequests).toBe(1);
  });

  it("preserves a guarded internal destination through login", async () => {
    useSignedOutSession();
    server.use(
      http.post("/api/v1/auth/login", () => HttpResponse.json(adminSession)),
    );

    renderAppAt("/members");
    expect(await screen.findByLabelText("Username")).toBeInTheDocument();
    await signIn();

    expect(
      await screen.findByRole("heading", { name: "Members" }),
    ).toBeInTheDocument();
  });

  it("redirects an authenticated login route to projects", async () => {
    server.use(
      http.get("/api/v1/auth/session", () => HttpResponse.json(adminSession)),
    );

    renderAppAt("/login");

    expect(
      await screen.findByRole("heading", { name: "Projects" }),
    ).toBeInTheDocument();
  });

  it("keeps admin navigation and routes unavailable to members", async () => {
    server.use(
      http.get("/api/v1/auth/session", () => HttpResponse.json(memberSession)),
    );

    renderAppAt("/system");

    expect(
      await screen.findByRole("heading", { name: "Projects" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Projects" })).toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: "Machine Access" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Members" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "System" })).not.toBeInTheDocument();
  });
});
