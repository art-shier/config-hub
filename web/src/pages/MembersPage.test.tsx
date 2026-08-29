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

function renderAt(path: string) {
  window.history.pushState({}, "", path);
  return render(<App />);
}

describe("MembersPage", () => {
  it("redirects non-admins without requesting the synchronized register", async () => {
    mockSession("member");
    let userRequests = 0;
    server.use(
      http.get("/api/v1/projects", () => HttpResponse.json({ projects: [] })),
      http.get("/api/v1/users", () => {
        userRequests += 1;
        return HttpResponse.json({ users: [], last_successful_user_sync_at: "2026-08-29T09:00:00Z" });
      }),
    );
    renderAt("/members");

    expect(await screen.findByRole("heading", { name: "Projects" })).toBeInTheDocument();
    expect(userRequests).toBe(0);
  });

  it("shows only read-only synchronized account status and no credentials", async () => {
    mockSession("admin");
    server.use(
      http.get("/api/v1/users", () =>
        HttpResponse.json({
          users: [
            {
              id: "user-dev",
              username: "developer-a",
              display_name: "开发者 A 🚀",
              role: "member",
              enabled: true,
              updated_at: "2026-08-29T08:30:00Z",
            },
            {
              id: "user-old",
              username: "former.operator",
              display_name: "Former Operator",
              role: "member",
              enabled: false,
              updated_at: "2026-08-28T08:30:00Z",
            },
          ],
          last_successful_user_sync_at: "2026-08-29T09:00:00Z",
          password: "must never render",
        }),
      ),
    );
    renderAt("/members");

    expect(await screen.findByRole("heading", { name: "Members" })).toBeInTheDocument();
    expect(await screen.findByText("developer-a")).toBeInTheDocument();
    expect(screen.getByText("开发者 A 🚀")).toBeInTheDocument();
    expect(screen.getByText("Enabled")).toBeInTheDocument();
    expect(screen.getByText("Disabled")).toBeInTheDocument();
    expect(screen.getByText(/Last successful user sync/iu)).toBeInTheDocument();
    expect(screen.queryByText("must never render")).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/password/iu)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /password|invite|register|enable|role/iu })).not.toBeInTheDocument();
  });

  it("shows a bounded unavailable state and retries", async () => {
    mockSession("admin");
    let requests = 0;
    server.use(
      http.get("/api/v1/users", () => {
        requests += 1;
        if (requests === 1) {
          return HttpResponse.json({ secret: "users.yaml contents" }, { status: 500 });
        }
        return HttpResponse.json({ users: [], last_successful_user_sync_at: "2026-08-29T09:00:00Z" });
      }),
    );
    renderAt("/members");
    const user = userEvent.setup();

    expect(await screen.findByRole("heading", { name: "Member register unavailable" })).toBeInTheDocument();
    expect(screen.queryByText("users.yaml contents")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Retry member register" }));
    expect(await screen.findByRole("heading", { name: "No synchronized accounts" })).toBeInTheDocument();
  });
});
