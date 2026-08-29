import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { App } from "../app/App";
import { server } from "../test/setup";

function mockSession(role: "admin" | "member") {
  server.use(
    http.get("/api/v1/auth/session", () =>
      HttpResponse.json({
        user: { id: `user-${role}`, username: role, display_name: role, role },
        csrf_token: `csrf-${role}`,
        expires_at: "2026-08-30T09:00:00Z",
      }),
    ),
  );
}

function renderAt(path = "/system") {
  window.history.pushState({}, "", path);
  return render(<App />);
}

describe("SystemPage", () => {
  it("redirects a non-admin without requesting system state", async () => {
    mockSession("member");
    let requests = 0;
    server.use(
      http.get("/api/v1/projects", () => HttpResponse.json({ projects: [] })),
      http.get("/api/v1/system", () => {
        requests += 1;
        return HttpResponse.json({});
      }),
    );
    renderAt();

    expect(await screen.findByRole("heading", { name: "Projects" })).toBeInTheDocument();
    expect(requests).toBe(0);
  });

  it("renders only explicitly labelled safe service state", async () => {
    mockSession("admin");
    server.use(
      http.get("/api/v1/system", () =>
        HttpResponse.json({
          build_version: "v0.15.0",
          live: true,
          ready: true,
          sqlite_ready: true,
          last_successful_user_sync_at: "2026-08-29T09:00:00Z",
          database_path: "/private/config-hub.db",
          users_file: "password: secret",
        }),
      ),
    );
    renderAt();

    expect(await screen.findByRole("heading", { name: "System" })).toBeInTheDocument();
    expect(await screen.findByText("Build version")).toBeInTheDocument();
    expect(screen.getByText("v0.15.0")).toBeInTheDocument();
    expect(screen.getByText("Live")).toBeInTheDocument();
    expect(screen.getByText("Ready")).toBeInTheDocument();
    expect(screen.getByText("SQLite readiness")).toBeInTheDocument();
    expect(screen.getAllByText("Available")).toHaveLength(3);
    expect(screen.getByText("Last successful user sync")).toBeInTheDocument();
    expect(screen.queryByText("/private/config-hub.db")).not.toBeInTheDocument();
    expect(screen.queryByText("password: secret")).not.toBeInTheDocument();
  });

  it("renders a safe unavailable state and retries", async () => {
    mockSession("admin");
    let requests = 0;
    server.use(
      http.get("/api/v1/system", () => {
        requests += 1;
        if (requests === 1) {
          return HttpResponse.json({ sqlite_error: "database is locked /private/secret.db" }, { status: 503 });
        }
        return HttpResponse.json({
          build_version: "dev",
          live: true,
          ready: false,
          sqlite_ready: true,
          last_successful_user_sync_at: "2026-08-29T09:00:00Z",
        });
      }),
    );
    renderAt();
    const user = userEvent.setup();

    expect(await screen.findByRole("heading", { name: "System state unavailable" })).toBeInTheDocument();
    expect(screen.queryByText(/private\/secret/iu)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Retry system state" }));
    expect(await screen.findByText("Unavailable")).toBeInTheDocument();
  });
});
