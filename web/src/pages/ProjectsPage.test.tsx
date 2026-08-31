import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { delay, http, HttpResponse } from "msw";
import { StrictMode } from "react";
import { describe, expect, it } from "vitest";
import { App } from "../app/App";
import { changeLocale } from "../i18n";
import { server } from "../test/setup";

type Role = "admin" | "member";

const shop = {
  id: "project-shop",
  slug: "shop",
  name: "Shop",
  description: "Storefront runtime configuration.",
  created_at: "2026-08-20T08:00:00Z",
  updated_at: "2026-08-29T08:30:00Z",
};

function mockSession({ role }: { role: Role }) {
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

function mockProjects(projects: (typeof shop)[]) {
  server.use(
    http.get("/api/v1/projects", () => HttpResponse.json({ projects })),
  );
}

function renderAppAt(path: string, { strict = false } = {}) {
  window.history.pushState({}, "", path);
  return render(
    strict ? (
      <StrictMode>
        <App />
      </StrictMode>
    ) : (
      <App />
    ),
  );
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
        message: "remote detail must not be displayed",
        request_id: "req-project",
        fields,
      },
    },
    { status },
  );
}

function mockCreateFailure() {
  server.use(
    http.post("/api/v1/projects", () =>
      HttpResponse.json(
        {
          error: {
            code: "validation_failed",
            message: "RAW MESSAGE",
            request_id: "req-project",
            fields: { slug: "RAW FIELD" },
          },
        },
        { status: 422 },
      ),
    ),
  );
}

describe("ProjectsPage", () => {
  it("renders and creates projects in Simplified Chinese without translating data", async () => {
    mockSession({ role: "admin" });
    mockProjects([shop]);
    await changeLocale("zh-CN");
    renderAppAt("/projects");

    expect(await screen.findByRole("heading", { name: "项目" })).toBeVisible();
    await userEvent.click(screen.getByRole("button", { name: "新建项目" }));
    expect(screen.getByLabelText("项目标识")).toBeVisible();
    expect(screen.getByText("Storefront runtime configuration.")).toBeVisible();
  });

  it("keeps the project description fixed-size with adequate writing space", async () => {
    mockSession({ role: "admin" });
    mockProjects([]);
    renderAppAt("/projects");
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "New project" }),
    );

    const description = screen.getByLabelText("Description");
    expect(description).toHaveClass("resize-none");
    expect(description).toHaveAttribute("rows", "4");
  });

  it("localizes project validation by field key and hides server text", async () => {
    mockSession({ role: "admin" });
    mockProjects([shop]);
    mockCreateFailure();
    await changeLocale("zh-CN");
    renderAppAt("/projects");
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "新建项目" }));
    await user.type(screen.getByLabelText("项目标识"), "shop");
    await user.type(screen.getByLabelText("项目名称"), "Shop");
    await user.click(screen.getByRole("button", { name: "创建项目" }));

    expect(await screen.findByText("项目标识不符合要求。")).toBeVisible();
    expect(screen.queryByText(/RAW/)).not.toBeInTheDocument();
  });

  it("does not render project creation for a member", async () => {
    mockSession({ role: "member" });
    mockProjects([shop]);
    renderAppAt("/projects", { strict: true });
    expect(await screen.findByText("Shop")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "New project" }),
    ).not.toBeInTheDocument();
  });

  it("renders only the projects made visible by the server as a linked register", async () => {
    mockSession({ role: "member" });
    mockProjects([shop]);

    renderAppAt("/projects");

    const projectLink = await screen.findByRole("link", { name: "Shop" });
    expect(projectLink).toHaveAttribute("href", "/projects/shop");
    expect(screen.getByText("shop")).toBeInTheDocument();
    expect(
      screen.getByText("Storefront runtime configuration."),
    ).toBeInTheDocument();
    expect(screen.getByText(/Updated.+2026/u)).toBeInTheDocument();
    expect(screen.queryByText("Hidden payroll")).not.toBeInTheDocument();
  });

  it("teaches the next step when there are no visible projects", async () => {
    mockSession({ role: "member" });
    mockProjects([]);

    renderAppAt("/projects");

    expect(
      await screen.findByRole("heading", { name: "No projects yet" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/administrator can grant access/u),
    ).toBeInTheDocument();
  });

  it("announces a failed project load and retries it successfully", async () => {
    mockSession({ role: "member" });
    const retryStarted = createDeferred<void>();
    const releaseRetry = createDeferred<void>();
    let requests = 0;
    server.use(
      http.get("/api/v1/projects", async () => {
        requests += 1;
        if (requests === 1) {
          return apiError(503, "service_unavailable");
        }
        retryStarted.resolve();
        await releaseRetry.promise;
        return HttpResponse.json({ projects: [shop] });
      }),
    );
    renderAppAt("/projects");
    const user = userEvent.setup();

    const retry = await screen.findByRole("button", { name: "Retry" });
    await user.click(retry);
    await retryStarted.promise;
    expect(screen.getByRole("status")).toHaveTextContent("Loading projects");

    releaseRetry.resolve();
    expect(
      await screen.findByRole("link", { name: "Shop" }),
    ).toBeInTheDocument();
    const result = screen.getByText("Projects loaded.");
    expect(result).toHaveAttribute("aria-live", "polite");
  });

  it("lets an admin create a project once with JSON and CSRF", async () => {
    mockSession({ role: "admin" });
    mockProjects([shop]);
    let requests = 0;
    let requestBody: unknown;
    let csrf = "";
    const created = {
      ...shop,
      id: "project-payments",
      slug: "payments",
      name: "Payments",
      description: "Payment controls",
    };
    server.use(
      http.post("/api/v1/projects", async ({ request }) => {
        requests += 1;
        csrf = request.headers.get("X-CSRF-Token") ?? "";
        requestBody = await request.json();
        await delay(40);
        return HttpResponse.json({ project: created }, { status: 201 });
      }),
    );

    renderAppAt("/projects", { strict: true });
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "New project" }),
    );
    const dialog = screen.getByRole("dialog", { name: "New project" });
    expect(within(dialog).getByLabelText("Project slug")).toHaveFocus();
    await user.type(within(dialog).getByLabelText("Project slug"), "payments");
    await user.type(within(dialog).getByLabelText("Project name"), "Payments");
    await user.type(
      within(dialog).getByLabelText("Description"),
      "Payment controls",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create project" }),
    );
    expect(
      within(dialog).getByRole("button", { name: "Creating project…" }),
    ).toBeDisabled();
    await user.click(
      within(dialog).getByRole("button", { name: "Creating project…" }),
    );

    expect(
      await screen.findByRole("link", { name: "Payments" }),
    ).toHaveAttribute("href", "/projects/payments");
    expect(requests).toBe(1);
    expect(csrf).toBe("csrf-admin");
    expect(requestBody).toEqual({
      slug: "payments",
      name: "Payments",
      description: "Payment controls",
    });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("restores the project dialog opener after Cancel and Escape", async () => {
    mockSession({ role: "admin" });
    mockProjects([shop]);
    renderAppAt("/projects");
    const user = userEvent.setup();
    const opener = await screen.findByRole("button", { name: "New project" });

    await user.click(opener);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(opener).toHaveFocus();

    await user.click(opener);
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(opener).toHaveFocus();
  });

  it("accepts a save after the project list updates behind the open dialog", async () => {
    mockSession({ role: "admin" });
    server.use(
      http.get("/api/v1/projects", async () => {
        await delay(80);
        return HttpResponse.json({ projects: [shop] });
      }),
      http.post("/api/v1/projects", () =>
        HttpResponse.json(
          {
            project: {
              ...shop,
              id: "project-payments",
              slug: "payments",
              name: "Payments",
            },
          },
          { status: 201 },
        ),
      ),
    );

    renderAppAt("/projects");
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "New project" }),
    );
    await user.type(screen.getByLabelText("Project slug"), "payments");
    await user.type(screen.getByLabelText("Project name"), "Payments");
    expect(
      await screen.findByRole("link", { name: "Shop" }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Create project" }));

    expect(
      await screen.findByRole("link", { name: "Payments" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("associates validation fields and retains the project draft", async () => {
    mockSession({ role: "admin" });
    mockProjects([shop]);
    server.use(
      http.post("/api/v1/projects", () =>
        apiError(422, "validation_failed", {
          slug: "Use lowercase letters, numbers, and hyphens.",
          name: "Enter a project name.",
        }),
      ),
    );

    renderAppAt("/projects");
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "New project" }),
    );
    await user.type(screen.getByLabelText("Project slug"), "Bad Slug");
    await user.type(screen.getByLabelText("Project name"), " ");
    await user.click(screen.getByRole("button", { name: "Create project" }));

    const slug = await screen.findByLabelText("Project slug");
    expect(slug).toHaveValue("Bad Slug");
    expect(slug).toHaveAccessibleDescription("Project slug is invalid.");
    expect(slug).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("Project name")).toHaveValue(" ");
  });

  it("keeps the list and draft after a duplicate or unavailable save", async () => {
    mockSession({ role: "admin" });
    mockProjects([shop]);
    let request = 0;
    server.use(
      http.post("/api/v1/projects", () => {
        request += 1;
        return request === 1
          ? apiError(409, "resource_conflict")
          : apiError(503, "service_unavailable");
      }),
    );

    renderAppAt("/projects");
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "New project" }),
    );
    await user.type(screen.getByLabelText("Project slug"), "shop");
    await user.type(screen.getByLabelText("Project name"), "Second shop");
    await user.click(screen.getByRole("button", { name: "Create project" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Choose another slug",
    );
    expect(screen.getByLabelText("Project name")).toHaveValue("Second shop");
    expect(screen.getByRole("link", { name: "Shop" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Create project" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "couldn’t be created",
    );
    expect(screen.getByLabelText("Project slug")).toHaveValue("shop");
    expect(screen.queryByText("remote detail")).not.toBeInTheDocument();
  });

  it("retains a failed draft and accepts a retry under StrictMode", async () => {
    mockSession({ role: "admin" });
    mockProjects([shop]);
    let requests = 0;
    server.use(
      http.post("/api/v1/projects", () => {
        requests += 1;
        return requests === 1
          ? apiError(503, "service_unavailable")
          : HttpResponse.json(
              {
                project: {
                  ...shop,
                  id: "project-payments",
                  slug: "payments",
                  name: "Payments",
                },
              },
              { status: 201 },
            );
      }),
    );

    renderAppAt("/projects", { strict: true });
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "New project" }),
    );
    await user.type(screen.getByLabelText("Project slug"), "payments");
    await user.type(screen.getByLabelText("Project name"), "Payments");
    await user.click(screen.getByRole("button", { name: "Create project" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "couldn’t be created",
    );
    expect(screen.getByLabelText("Project slug")).toHaveValue("payments");
    expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Create project" }));

    expect(
      await screen.findByRole("link", { name: "Payments" }),
    ).toBeInTheDocument();
    expect(requests).toBe(2);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("keeps an uncertain project creation open until its response arrives", async () => {
    mockSession({ role: "admin" });
    mockProjects([shop]);
    const requestStarted = createDeferred<void>();
    const releaseRequest = createDeferred<void>();
    server.use(
      http.post("/api/v1/projects", async () => {
        requestStarted.resolve();
        await releaseRequest.promise;
        return HttpResponse.json(
          {
            project: {
              ...shop,
              id: "project-payments",
              slug: "payments",
              name: "Payments",
            },
          },
          { status: 201 },
        );
      }),
    );

    renderAppAt("/projects", { strict: true });
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("button", { name: "New project" }),
    );
    await user.type(screen.getByLabelText("Project slug"), "payments");
    await user.type(screen.getByLabelText("Project name"), "Payments");
    await user.click(screen.getByRole("button", { name: "Create project" }));
    await requestStarted.promise;

    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(
      screen.getByRole("dialog", { name: "New project" }),
    ).toBeInTheDocument();

    releaseRequest.resolve();
    expect(
      await screen.findByRole("link", { name: "Payments" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
