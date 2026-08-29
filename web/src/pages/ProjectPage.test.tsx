import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { StrictMode } from "react";
import { describe, expect, it } from "vitest";
import { App } from "../app/App";
import { server } from "../test/setup";

type Role = "admin" | "member";
type Permission = "admin" | "viewer" | "editor";

const environments = [
  {
    id: "env-prod",
    project_id: "project-shop",
    slug: "prod",
    name: "Production",
    current_revision_id: "revision-7",
    created_at: "2026-08-20T08:00:00Z",
    updated_at: "2026-08-29T08:30:00Z",
  },
  {
    id: "env-stage",
    project_id: "project-shop",
    slug: "stage",
    name: "Staging",
    current_revision_id: null,
    created_at: "2026-08-21T08:00:00Z",
    updated_at: "2026-08-28T08:30:00Z",
  },
];

function mockProjectPage(
  role: Role,
  permission: Permission,
  projectEnvironments = environments,
) {
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
    http.get("/api/v1/projects/shop", () =>
      HttpResponse.json({
        project: {
          id: "project-shop",
          slug: "shop",
          name: "Shop",
          description: "Storefront runtime configuration.",
          created_at: "2026-08-20T08:00:00Z",
          updated_at: "2026-08-29T08:30:00Z",
          permission,
          environments: projectEnvironments,
        },
      }),
    ),
  );
}

function renderAppAt(path: string, { strict = false } = {}) {
  window.history.pushState({}, "", path);
  return render(strict ? <StrictMode><App /></StrictMode> : <App />);
}

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>((promiseResolve) => {
    resolve = promiseResolve;
  });
  return { promise, resolve };
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
        message: "remote project SECRET detail",
        request_id: "req-detail",
        fields,
      },
    },
    { status },
  );
}

describe("ProjectPage", () => {
  it("renders project metadata, environment state, and semantic tabs", async () => {
    mockProjectPage("admin", "admin");

    renderAppAt("/projects/shop", { strict: true });

    expect(await screen.findByRole("heading", { name: "Shop" })).toBeInTheDocument();
    expect(screen.getByText("Storefront runtime configuration.")).toBeInTheDocument();
    expect(screen.getByText("shop")).toBeInTheDocument();
    expect(screen.getAllByText("Production")).toHaveLength(2);
    expect(screen.getByText("Current revision revision-7")).toBeInTheDocument();
    expect(screen.getByText("No revision published")).toBeInTheDocument();
    const tabs = screen.getByRole("tablist", { name: "Project sections" });
    expect(within(tabs).getByRole("tab", { name: "Configuration" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    for (const [name, id] of [
      ["Configuration", "configuration"],
      ["Versions", "versions"],
      ["Members", "members"],
    ] as const) {
      expect(within(tabs).getByRole("tab", { name })).toHaveAttribute(
        "aria-controls",
        `project-panel-${id}`,
      );
      expect(document.getElementById(`project-panel-${id}`)).toBeInTheDocument();
    }
    await waitFor(() => expect(window.location.search).toBe("?environment=prod"));
  });

  it("uses roving focus with automatic tab selection for arrows, Home, and End", async () => {
    mockProjectPage("admin", "admin");
    server.use(
      http.get("/api/v1/projects/shop/members", () =>
        HttpResponse.json({ members: [] }),
      ),
    );
    renderAppAt("/projects/shop");
    const user = userEvent.setup();
    const configuration = await screen.findByRole("tab", {
      name: "Configuration",
    });
    const versions = screen.getByRole("tab", { name: "Versions" });
    const members = screen.getByRole("tab", { name: "Members" });
    configuration.focus();

    expect(configuration).toHaveAttribute("tabindex", "0");
    expect(versions).toHaveAttribute("tabindex", "-1");
    await user.keyboard("{ArrowRight}");
    expect(versions).toHaveFocus();
    expect(versions).toHaveAttribute("aria-selected", "true");
    expect(versions).toHaveAttribute("tabindex", "0");
    expect(configuration).toHaveAttribute("tabindex", "-1");
    expect(document.getElementById("project-panel-versions")).not.toHaveAttribute(
      "hidden",
    );
    expect(document.getElementById("project-panel-configuration")).toHaveAttribute(
      "hidden",
    );

    await user.keyboard("{End}");
    expect(members).toHaveFocus();
    expect(members).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{Home}");
    expect(configuration).toHaveFocus();
    expect(configuration).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{ArrowLeft}");
    expect(members).toHaveFocus();
    expect(members).toHaveAttribute("aria-selected", "true");
  });

  it("preserves valid environment selection while switching tabs and sanitizes invalid values", async () => {
    mockProjectPage("admin", "admin");
    renderAppAt("/projects/shop?environment=missing&extra=discarded");

    expect(await screen.findByRole("heading", { name: "Shop" })).toBeInTheDocument();
    await waitFor(() => expect(window.location.search).toBe("?environment=prod"));
    const user = userEvent.setup();
    await user.selectOptions(screen.getByLabelText("Active environment"), "stage");
    expect(window.location.search).toBe("?environment=stage");

    await user.click(screen.getByRole("tab", { name: "Versions" }));
    expect(window.location.search).toBe("?environment=stage&tab=versions");
    expect(screen.getByRole("tab", { name: "Versions" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText(/Version history arrives in Task 14/u)).toBeInTheDocument();
  });

  it("renders the current grant register on the Members tab", async () => {
    mockProjectPage("admin", "admin");
    server.use(
      http.get("/api/v1/projects/shop/members", () =>
        HttpResponse.json({
          members: [
            {
              user_id: "user-alex",
              username: "alex.smith",
              display_name: "Alex Smith",
              permission: "viewer",
            },
          ],
        }),
      ),
    );

    renderAppAt("/projects/shop?environment=prod&tab=members");

    expect(
      await screen.findByRole("heading", { name: "Project members" }),
    ).toBeInTheDocument();
    expect(await screen.findByText("Alex Smith")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add member" })).toBeInTheDocument();
    expect(window.location.search).toBe("?environment=prod&tab=members");
  });

  it.each(["viewer", "editor"] as const)(
    "keeps %s project access free of administration actions",
    async (permission) => {
    mockProjectPage("member", permission);

    renderAppAt("/projects/shop");

    expect(await screen.findByRole("heading", { name: "Shop" })).toBeInTheDocument();
    expect(screen.getByText(new RegExp(`${permission} access`, "iu"))).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "New environment" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add member" })).not.toBeInTheDocument();
    },
  );

  it("lets an admin create an environment with inline validation and retained draft", async () => {
    mockProjectPage("admin", "admin");
    let requests = 0;
    let body: unknown;
    let csrf = "";
    server.use(
      http.post("/api/v1/projects/shop/environments", async ({ request }) => {
        requests += 1;
        body = await request.json();
        csrf = request.headers.get("X-CSRF-Token") ?? "";
        if (requests === 1) {
          return apiError(422, "validation_failed", {
            slug: "Use a lowercase environment slug.",
          });
        }
        return HttpResponse.json(
          {
            environment: {
              ...environments[1],
              id: "env-preview",
              slug: "preview",
              name: "Preview",
            },
          },
          { status: 201 },
        );
      }),
    );

    renderAppAt("/projects/shop", { strict: true });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "New environment" }));
    expect(screen.getByLabelText("Environment slug")).toHaveFocus();
    await user.type(screen.getByLabelText("Environment slug"), "Preview");
    await user.type(screen.getByLabelText("Environment name"), "Preview");
    await user.click(screen.getByRole("button", { name: "Create environment" }));

    const slug = await screen.findByLabelText("Environment slug");
    expect(slug).toHaveValue("Preview");
    expect(slug).toHaveAccessibleDescription("Use a lowercase environment slug.");
    await user.clear(slug);
    await user.type(slug, "preview");
    await user.click(screen.getByRole("button", { name: "Create environment" }));

    expect(await screen.findAllByText("Preview")).toHaveLength(2);
    expect(requests).toBe(2);
    expect(body).toEqual({ slug: "preview", name: "Preview" });
    expect(csrf).toBe("csrf-admin");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("keeps an uncertain environment creation open until its response arrives", async () => {
    mockProjectPage("admin", "admin");
    const requestStarted = createDeferred<void>();
    const releaseRequest = createDeferred<void>();
    server.use(
      http.post("/api/v1/projects/shop/environments", async () => {
        requestStarted.resolve();
        await releaseRequest.promise;
        return HttpResponse.json(
          {
            environment: {
              ...environments[1],
              id: "env-preview",
              slug: "preview",
              name: "Preview",
            },
          },
          { status: 201 },
        );
      }),
    );

    renderAppAt("/projects/shop", { strict: true });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "New environment" }));
    await user.type(screen.getByLabelText("Environment slug"), "preview");
    await user.type(screen.getByLabelText("Environment name"), "Preview");
    await user.click(screen.getByRole("button", { name: "Create environment" }));
    await requestStarted.promise;

    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(
      screen.getByRole("dialog", { name: "New environment" }),
    ).toBeInTheDocument();

    releaseRequest.resolve();
    expect(await screen.findAllByText("Preview")).toHaveLength(2);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("retains an environment draft and current list after conflict", async () => {
    mockProjectPage("admin", "admin");
    server.use(
      http.post("/api/v1/projects/shop/environments", () =>
        apiError(409, "resource_conflict"),
      ),
    );

    renderAppAt("/projects/shop");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "New environment" }));
    await user.type(screen.getByLabelText("Environment slug"), "prod");
    await user.type(screen.getByLabelText("Environment name"), "Second prod");
    await user.click(screen.getByRole("button", { name: "Create environment" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Choose another slug");
    expect(screen.getByLabelText("Environment name")).toHaveValue("Second prod");
    expect(screen.getAllByText("Production")).toHaveLength(2);
    expect(screen.queryByText("SECRET")).not.toBeInTheDocument();
  });

  it.each([
    [404, "not_found", "Project not found"],
    [503, "service_unavailable", "Project unavailable"],
  ])("renders a safe %i load state", async (status, code, heading) => {
    server.use(
      http.get("/api/v1/auth/session", () =>
        HttpResponse.json({
          user: {
            id: "user-admin",
            username: "admin",
            display_name: "Ada Lovelace",
            role: "admin",
          },
          csrf_token: "csrf-admin",
          expires_at: "2026-08-30T09:00:00Z",
        }),
      ),
      http.get("/api/v1/projects/shop", () => apiError(status, code)),
    );

    renderAppAt("/projects/shop");

    expect(await screen.findByRole("heading", { name: heading })).toBeInTheDocument();
    expect(screen.queryByText("SECRET")).not.toBeInTheDocument();
  });

  it("announces an unavailable project load and retries it successfully", async () => {
    const retryStarted = createDeferred<void>();
    const releaseRetry = createDeferred<void>();
    let requests = 0;
    mockProjectPage("admin", "admin");
    server.use(
      http.get("/api/v1/projects/shop", async () => {
        requests += 1;
        if (requests === 1) {
          return apiError(503, "service_unavailable");
        }
        retryStarted.resolve();
        await releaseRetry.promise;
        return HttpResponse.json({
          project: {
            id: "project-shop",
            slug: "shop",
            name: "Shop",
            description: "Storefront runtime configuration.",
            created_at: "2026-08-20T08:00:00Z",
            updated_at: "2026-08-29T08:30:00Z",
            permission: "admin",
            environments,
          },
        });
      }),
    );
    renderAppAt("/projects/shop");
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Retry" }));
    await retryStarted.promise;
    expect(screen.getByRole("status")).toHaveTextContent("Loading project");
    releaseRetry.resolve();

    expect(await screen.findByRole("heading", { name: "Shop" })).toBeInTheDocument();
    const result = screen.getByText("Project loaded.");
    expect(result).toHaveAttribute("aria-live", "polite");
  });

  it("discards a late project retry after navigation selects another project", async () => {
    const retryStarted = createDeferred<void>();
    const releaseRetry = createDeferred<void>();
    let shopRequests = 0;
    mockProjectPage("admin", "admin");
    server.use(
      http.get("/api/v1/projects/shop", async () => {
        shopRequests += 1;
        if (shopRequests === 1) {
          return apiError(503, "service_unavailable");
        }
        retryStarted.resolve();
        await releaseRetry.promise;
        return HttpResponse.json({
          project: {
            id: "project-shop",
            slug: "shop",
            name: "Stale Shop",
            description: "stale",
            created_at: "2026-08-20T08:00:00Z",
            updated_at: "2026-08-29T08:30:00Z",
            permission: "admin",
            environments,
          },
        });
      }),
      http.get("/api/v1/projects/billing", () =>
        HttpResponse.json({
          project: {
            id: "project-billing",
            slug: "billing",
            name: "Billing",
            description: "Billing controls",
            created_at: "2026-08-20T08:00:00Z",
            updated_at: "2026-08-29T08:30:00Z",
            permission: "admin",
            environments: [],
          },
        }),
      ),
    );
    renderAppAt("/projects/shop");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Retry" }));
    await retryStarted.promise;

    window.history.pushState({}, "", "/projects/billing");
    window.dispatchEvent(new PopStateEvent("popstate"));
    expect(await screen.findByRole("heading", { name: "Billing" })).toBeInTheDocument();
    releaseRetry.resolve();

    await waitFor(() =>
      expect(screen.queryByRole("heading", { name: "Stale Shop" })).not.toBeInTheDocument(),
    );
    expect(screen.getByRole("heading", { name: "Billing" })).toBeInTheDocument();
  });
});
