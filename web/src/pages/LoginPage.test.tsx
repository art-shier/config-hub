import { readFileSync } from "node:fs";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { delay, http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
} from "react-router-dom";
import { App } from "../app/App";
import { AuthProvider } from "../auth/AuthProvider";
import { changeLocale } from "../i18n";
import { server } from "../test/setup";
import { LoginPage } from "./LoginPage";

const styles = readFileSync("src/styles.css", "utf8");

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

function LocationProbe() {
  const location = useLocation();
  return (
    <output aria-label="Current location">
      {location.pathname}
      {location.search}
    </output>
  );
}

function renderLoginWithDestination(pathname: string, search = "") {
  return render(
    <MemoryRouter
      initialEntries={[
        {
          pathname: "/login",
          state: { from: { pathname, search } },
        },
      ]}
    >
      <AuthProvider>
        <LocationProbe />
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="*" element={<p>Destination route</p>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
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
  // Break caught: remounting login while applying a locale, which loses an in-progress username.
  it("keeps login field values while switching language", async () => {
    useSignedOutSession();
    const user = userEvent.setup();
    renderAppAt("/login");

    await user.type(await screen.findByLabelText("Username"), "admin");
    await user.selectOptions(screen.getByRole("combobox", { name: "Language" }), "zh-CN");

    expect(screen.getByLabelText("\u7528\u6237\u540d")).toHaveValue("admin");
    expect(screen.getByRole("button", { name: "\u767b\u5f55" })).toBeVisible();
  });

  // Break caught: rendering an API-provided login error message instead of its stable status/code mapping.
  it("does not disclose a raw API login message in Chinese", async () => {
    useSignedOutSession();
    server.use(
      http.post("/api/v1/auth/login", () =>
        HttpResponse.json(
          {
            error: {
              code: "invalid_credentials",
              message: "RAW SECRET",
              request_id: "req",
              fields: {},
            },
          },
          { status: 401 },
        ),
      ),
    );
    await changeLocale("zh-CN");
    renderAppAt("/login");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("\u7528\u6237\u540d"), "admin");
    await user.type(screen.getByLabelText("\u5bc6\u7801"), "wrong");
    await user.click(screen.getByRole("button", { name: "\u767b\u5f55" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("\u7528\u6237\u540d\u6216\u5bc6\u7801\u4e0d\u6b63\u786e\u3002");
    expect(screen.queryByText("RAW SECRET")).not.toBeInTheDocument();
  });

  // Break caught: changing password visibility without an accessible stateful control, or exposing it by default.
  it("keeps the password masked until the accessible visibility control is activated", async () => {
    useSignedOutSession();
    const user = userEvent.setup();
    renderAppAt("/login");
    const password = await screen.findByLabelText("Username").then(() => screen.getByLabelText("Password"));
    await user.type(password, "secret");

    expect(password).toHaveAttribute("type", "password");
    await user.click(screen.getByRole("button", { name: "Show password" }));
    expect(password).toHaveAttribute("type", "text");
    expect(password).toHaveValue("secret");
    expect(screen.getByRole("button", { name: "Hide password" })).toHaveAttribute("aria-pressed", "true");
  });

  // Break caught: allowing browser-native validation UI to replace localized login recovery.
  it("owns login validation with noValidate", async () => {
    useSignedOutSession();
    renderAppAt("/login");

    expect((await screen.findByLabelText("Username")).closest("form")).toHaveAttribute("novalidate");
  });
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

  it("preserves a project detail destination and its safe query", async () => {
    useSignedOutSession();
    server.use(
      http.post("/api/v1/auth/login", () => HttpResponse.json(adminSession)),
    );

    renderAppAt("/projects/shop?environment=prod&tab=versions");
    expect(await screen.findByLabelText("Username")).toBeInTheDocument();
    await signIn();

    await waitFor(() => {
      expect(window.location.pathname).toBe("/projects/shop");
      expect(window.location.search).toBe("?environment=prod&tab=versions");
    });
  });

  it("preserves a project slug at the 63-character limit", async () => {
    const projectPath = `/projects/${"a".repeat(63)}`;
    useSignedOutSession();
    server.use(
      http.post("/api/v1/auth/login", () => HttpResponse.json(adminSession)),
    );

    renderLoginWithDestination(projectPath);
    await signIn();

    await waitFor(() =>
      expect(screen.getByLabelText("Current location")).toHaveTextContent(
        projectPath,
      ),
    );
  });

  it.each([
    ["protocol-relative path", "//attacker.example"],
    ["backslash path", "/projects\\shop"],
    ["dot segment", "/projects/../system"],
    ["encoded dot segment", "/projects/%2e%2e/system"],
    ["encoded slash", "/projects/shop%2Fsystem"],
    ["encoded backslash", "/projects/shop%5Csystem"],
    ["extra project segment", "/projects/shop/settings"],
    ["admin path concatenation", "/system/audit"],
    ["unknown path", "/unknown"],
    ["invalid project slug", "/projects/-shop"],
    ["uppercase project slug", "/projects/Shop"],
    ["underscore project slug", "/projects/shop_api"],
    ["overlong project slug", `/projects/${"a".repeat(64)}`],
  ])("rejects unsafe login destination: %s", async (_label, pathname) => {
    useSignedOutSession();
    server.use(
      http.post("/api/v1/auth/login", () => HttpResponse.json(adminSession)),
    );

    renderLoginWithDestination(pathname, "?tab=versions");
    await signIn();

    await waitFor(() =>
      expect(screen.getByLabelText("Current location")).toHaveTextContent(
        "/projects",
      ),
    );
    expect(screen.getByLabelText("Current location")).toHaveTextContent(
      /^\/projects$/,
    );
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

  it("keeps the session visible and allows retry after logout is unconfirmed", async () => {
    let logoutRequests = 0;
    server.use(
      http.get("/api/v1/auth/session", () => HttpResponse.json(adminSession)),
      http.post("/api/v1/auth/logout", () => {
        logoutRequests += 1;
        if (logoutRequests === 1) {
          return HttpResponse.json(
            {
              error: {
                code: "service_unavailable",
                message: "internal upstream SECRET",
                request_id: "req_logout_retry",
                fields: { csrf_token: "csrf-login-token" },
              },
            },
            { status: 503 },
          );
        }
        return new HttpResponse(null, { status: 204 });
      }),
    );

    renderAppAt("/projects");
    expect(
      await screen.findByRole("heading", { name: "Projects" }),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Sign out" }));

    const error = await screen.findByRole("status");
    expect(error).toHaveAttribute("aria-live", "polite");
    expect(error).toHaveAttribute("aria-atomic", "true");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(error).toHaveTextContent(
      "ConfigHub couldn’t confirm sign-out. You’re still signed in. Check the server and try again.",
    );
    expect(error).not.toHaveTextContent("upstream");
    expect(error).not.toHaveTextContent("csrf-login-token");
    expect(screen.getByText("Ada Lovelace")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sign out" })).toBeEnabled();

    await userEvent.click(screen.getByRole("button", { name: "Sign out" }));

    expect(await screen.findByLabelText("Username")).toBeInTheDocument();
    expect(window.location.pathname).toBe("/login");
    expect(logoutRequests).toBe(2);
  });

  it.each([
    {
      outcome: "a malformed 401",
      respond: () =>
        new HttpResponse("<html>logout upstream SECRET</html>", {
          status: 401,
          headers: { "Content-Type": "text/html" },
        }),
    },
    {
      outcome: "a non-invalid-session 401",
      respond: () =>
        HttpResponse.json(
          {
            error: {
              code: "permission_denied",
              message: "logout policy SECRET",
              request_id: "req_logout_permission",
              fields: { csrf_token: "csrf-body-secret" },
            },
          },
          { status: 401 },
        ),
    },
    {
      outcome: "an empty 200",
      respond: () => new HttpResponse(null, { status: 200 }),
    },
  ])(
    "reconciles $outcome instead of treating logout as confirmed",
    async ({ respond }) => {
      let sessionRequests = 0;
      server.use(
        http.get("/api/v1/auth/session", () => {
          sessionRequests += 1;
          return HttpResponse.json(adminSession);
        }),
        http.post("/api/v1/auth/logout", respond),
      );

      renderAppAt("/projects");
      expect(
        await screen.findByRole("heading", { name: "Projects" }),
      ).toBeInTheDocument();

      await userEvent.click(screen.getByRole("button", { name: "Sign out" }));

      const status = await screen.findByRole("status");
      expect(status).toHaveTextContent(
        "ConfigHub couldn’t confirm sign-out. You’re still signed in. Check the server and try again.",
      );
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      expect(document.body).not.toHaveTextContent("SECRET");
      expect(document.body).not.toHaveTextContent("csrf-body-secret");
      expect(screen.getByText("Ada Lovelace")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Sign out" })).toBeEnabled();
      expect(window.location.pathname).toBe("/projects");
      expect(sessionRequests).toBe(2);
    },
  );

  it("returns to login when a current authenticated request receives 401", async () => {
    server.use(
      http.get("/api/v1/auth/session", () => HttpResponse.json(adminSession)),
      http.post("/api/v1/auth/logout", () =>
        HttpResponse.json(
          {
            error: {
              code: "invalid_session",
              message: "expired",
              request_id: "req_logout",
              fields: {},
            },
          },
          { status: 401 },
        ),
      ),
    );

    renderAppAt("/projects");
    expect(
      await screen.findByRole("heading", { name: "Projects" }),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Sign out" }));

    expect(await screen.findByLabelText("Username")).toBeInTheDocument();
    expect(window.location.pathname).toBe("/login");
  });
});

describe("responsive authentication styles", () => {
  it("does not force narrow text-zoomed viewports wider than the page", () => {
    expect(styles).toContain(":root");
    expect(styles).not.toMatch(/\bhtml\s*\{[^}]*\bmin-width\s*:/u);
    expect(styles).not.toMatch(/\bbody\s*\{[^}]*\bmin-width\s*:/u);
  });

  it("wraps logout feedback within the session summary", () => {
    expect(styles).toMatch(
      /\.logout-error\s*\{[^}]*\boverflow-wrap\s*:\s*anywhere/u,
    );
  });
});
