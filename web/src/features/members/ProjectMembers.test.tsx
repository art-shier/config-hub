import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { AuthProvider } from "../../auth/AuthProvider";
import { server } from "../../test/setup";
import { ProjectMembers } from "./ProjectMembers";

type Role = "admin" | "member";
type TestMember = {
  user_id: string;
  username: string;
  display_name: string;
  permission: "viewer" | "editor";
};

const alex = {
  user_id: "user-alex",
  username: "alex.smith",
  display_name: "Alex Smith",
  permission: "viewer" as const,
};

function mockSession(role: Role) {
  server.use(
    http.get("/api/v1/auth/session", () =>
      HttpResponse.json({
        user: {
          id: `user-${role}`,
          username: role,
          display_name: role === "admin" ? "Ada Lovelace" : "Lee Operator",
          role,
        },
        csrf_token: `csrf-${role}`,
        expires_at: "2026-08-30T09:00:00Z",
      }),
    ),
  );
}

function mockMembers(members: TestMember[] = [alex]) {
  server.use(
    http.get("/api/v1/projects/shop/members", () =>
      HttpResponse.json({ members }),
    ),
  );
}

function renderMembers(role: Role, canManage: boolean) {
  mockSession(role);
  return render(
    <MemoryRouter>
      <AuthProvider>
        <ProjectMembers projectSlug="shop" canManage={canManage} />
      </AuthProvider>
    </MemoryRouter>,
  );
}

function apiError(
  status: number,
  code: string,
  fields: Record<string, string> = {},
) {
  return HttpResponse.json(
    {
      error: {
        code,
        message: "remote membership SECRET detail",
        request_id: "req-members",
        fields,
      },
    },
    { status },
  );
}

describe("ProjectMembers", () => {
  it("lists current grants without inventing a user directory", async () => {
    mockMembers();
    renderMembers("member", false);

    expect(await screen.findByText("Alex Smith")).toBeInTheDocument();
    expect(screen.getByText("@alex.smith")).toBeInTheDocument();
    expect(screen.getByText("Viewer")).toBeInTheDocument();
    expect(screen.getByText(/project administrators can change access/iu)).toBeInTheDocument();
    expect(screen.queryByLabelText("Synchronized username")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove access" })).not.toBeInTheDocument();
  });

  it("adds a synchronized username with encoded path, JSON, CSRF, and a refreshed grant", async () => {
    let currentMembers: TestMember[] = [alex];
    let putPath = "";
    let putBody: unknown;
    let csrf = "";
    mockMembers(currentMembers);
    server.use(
      http.put("/api/v1/projects/shop/members/:username", async ({ params, request }) => {
        putPath = String(params.username);
        putBody = await request.json();
        csrf = request.headers.get("X-CSRF-Token") ?? "";
        currentMembers = [
          ...currentMembers,
          {
            user_id: "user-jane",
            username: "jane.doe",
            display_name: "Jane Doe",
            permission: "editor" as const,
          },
        ];
        return new HttpResponse(null, { status: 204 });
      }),
      http.get("/api/v1/projects/shop/members", () =>
        HttpResponse.json({ members: currentMembers }),
      ),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    await screen.findByText("Alex Smith");

    await user.type(screen.getByLabelText("Synchronized username"), "jane.doe");
    await user.selectOptions(screen.getByLabelText("New member permission"), "editor");
    await user.click(screen.getByRole("button", { name: "Add member" }));

    expect(await screen.findByText("Jane Doe")).toBeInTheDocument();
    expect(putPath).toBe("jane.doe");
    expect(putBody).toEqual({ permission: "editor" });
    expect(csrf).toBe("csrf-admin");
    expect(screen.getByLabelText("Synchronized username")).toHaveValue("");
  });

  it("validates a username before building the request path", async () => {
    let putRequests = 0;
    mockMembers();
    server.use(
      http.put("/api/v1/projects/shop/members/:username", () => {
        putRequests += 1;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    await screen.findByText("Alex Smith");

    await user.type(screen.getByLabelText("Synchronized username"), "../admin");
    await user.click(screen.getByRole("button", { name: "Add member" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Enter a valid synchronized username",
    );
    expect(screen.getByLabelText("Synchronized username")).toHaveValue("../admin");
    expect(putRequests).toBe(0);
  });

  it("retains add fields and current grants after server validation", async () => {
    mockMembers();
    server.use(
      http.put("/api/v1/projects/shop/members/:username", () =>
        apiError(422, "validation_failed", {
          username: "The synchronized user is disabled.",
        }),
      ),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    await screen.findByText("Alex Smith");

    await user.type(screen.getByLabelText("Synchronized username"), "disabled.user");
    await user.selectOptions(screen.getByLabelText("New member permission"), "editor");
    await user.click(screen.getByRole("button", { name: "Add member" }));

    const username = await screen.findByLabelText("Synchronized username");
    expect(username).toHaveValue("disabled.user");
    expect(username).toHaveAccessibleDescription("The synchronized user is disabled.");
    expect(screen.getByLabelText("New member permission")).toHaveValue("editor");
    expect(screen.getByText("Alex Smith")).toBeInTheDocument();
    expect(screen.queryByText("SECRET")).not.toBeInTheDocument();
  });

  it("retains a changed grant draft and current list when save fails", async () => {
    mockMembers();
    server.use(
      http.put("/api/v1/projects/shop/members/alex.smith", () =>
        apiError(503, "service_unavailable"),
      ),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    const row = await screen.findByRole("listitem", { name: "Alex Smith access" });

    await user.selectOptions(within(row).getByLabelText("Permission for alex.smith"), "editor");
    await user.click(within(row).getByRole("button", { name: "Save permission" }));

    expect(await within(row).findByRole("alert")).toHaveTextContent(
      "Permission wasn’t saved",
    );
    expect(within(row).getByLabelText("Permission for alex.smith")).toHaveValue("editor");
    expect(screen.getByText("Alex Smith")).toBeInTheDocument();
  });

  it("changes an existing viewer grant to editor and confirms the saved permission", async () => {
    let putPath = "";
    let putBody: unknown;
    mockMembers();
    server.use(
      http.put(
        "/api/v1/projects/shop/members/alex.smith",
        async ({ request }) => {
          putPath = new URL(request.url).pathname;
          putBody = await request.json();
          return new HttpResponse(null, { status: 204 });
        },
      ),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    const row = await screen.findByRole("listitem", {
      name: "Alex Smith access",
    });
    const permission = within(row).getByLabelText(
      "Permission for alex.smith",
    );
    expect(permission).toHaveValue("viewer");

    await user.selectOptions(permission, "editor");
    await user.click(
      within(row).getByRole("button", { name: "Save permission" }),
    );

    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent(
        "Permission for Alex Smith updated to Editor.",
      ),
    );
    expect(putPath).toBe("/api/v1/projects/shop/members/alex.smith");
    expect(putBody).toEqual({ permission: "editor" });
    expect(permission).toHaveValue("editor");
  });

  it("requires named confirmation, preserves it on failure, and retries removal", async () => {
    let removeRequests = 0;
    mockMembers();
    server.use(
      http.delete("/api/v1/projects/shop/members/alex.smith", () => {
        removeRequests += 1;
        return removeRequests === 1
          ? apiError(503, "service_unavailable")
          : new HttpResponse(null, { status: 204 });
      }),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    const row = await screen.findByRole("listitem", { name: "Alex Smith access" });

    await user.click(within(row).getByRole("button", { name: "Remove access" }));
    let dialog = screen.getByRole("dialog", { name: "Remove Alex Smith access" });
    expect(within(dialog).getByText(/Alex Smith \(@alex\.smith\)/u)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByText("Alex Smith")).toBeInTheDocument();
    expect(removeRequests).toBe(0);

    await user.click(within(row).getByRole("button", { name: "Remove access" }));
    dialog = screen.getByRole("dialog", { name: "Remove Alex Smith access" });
    await user.click(within(dialog).getByRole("button", { name: "Remove access" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Access wasn’t removed",
    );
    expect(screen.getByText("Alex Smith")).toBeInTheDocument();
    expect(screen.queryByText("SECRET")).not.toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Remove access" }));
    await waitFor(() => expect(screen.queryByText("Alex Smith")).not.toBeInTheDocument());
    expect(removeRequests).toBe(2);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
