import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { App } from "./App";
import { server } from "../test/setup";
import { changeLocale } from "../i18n";

function mockAdminShell() {
  server.use(
    http.get("/api/v1/auth/session", () =>
      HttpResponse.json({
        user: { id: "admin-id", username: "admin", display_name: "Ada", role: "admin" },
        csrf_token: "csrf-admin",
        expires_at: "2026-08-30T09:00:00Z",
      }),
    ),
    http.get("/api/v1/projects", () => HttpResponse.json({ projects: [] })),
    http.get("/api/v1/users", () => HttpResponse.json({ users: [], last_successful_user_sync_at: "2026-08-29T09:00:00Z" })),
  );
}

describe("AppShell responsive navigation", () => {
  // Break caught: leaving authenticated navigation labels and the route title in the previous locale.
  it("localizes shared navigation and the current route title", async () => {
    mockAdminShell();
    window.history.pushState({}, "", "/projects");
    await changeLocale("zh-CN");
    render(<App />);

    expect(await screen.findByRole("link", { name: "\u9879\u76ee" })).toBeInTheDocument();
    expect(document.title).toBe("ConfigHub \u2014 \u9879\u76ee");
  });
  it("opens an accessible menu and restores the control on Escape", async () => {
    mockAdminShell();
    window.history.pushState({}, "", "/projects");
    render(<App />);
    const user = userEvent.setup();
    const menu = await screen.findByRole("button", { name: "Open navigation" });

    expect(menu).toHaveAttribute("aria-expanded", "false");
    await user.click(menu);
    expect(menu).toHaveAttribute("aria-expanded", "true");
    expect(menu).toHaveAccessibleName("Close navigation");
    expect(screen.getByRole("link", { name: "Projects" })).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(menu).toHaveAttribute("aria-expanded", "false");
    expect(menu).toHaveAccessibleName("Open navigation");
    expect(menu).toHaveFocus();
  });

  it("closes after outside interaction and navigation selection", async () => {
    mockAdminShell();
    window.history.pushState({}, "", "/projects");
    render(<App />);
    const user = userEvent.setup();
    const menu = await screen.findByRole("button", { name: "Open navigation" });

    await user.click(menu);
    await user.click(screen.getByText("Ada"));
    expect(menu).toHaveAttribute("aria-expanded", "false");

    await user.click(menu);
    await user.click(screen.getByRole("link", { name: "Members" }));
    await waitFor(() => expect(window.location.pathname).toBe("/members"));
    expect(menu).toHaveAttribute("aria-expanded", "false");
  });
});
