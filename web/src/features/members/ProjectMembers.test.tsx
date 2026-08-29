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

function membersTree(projectSlug: string, canManage: boolean) {
  return (
    <MemoryRouter>
      <AuthProvider>
        <ProjectMembers projectSlug={projectSlug} canManage={canManage} />
      </AuthProvider>
    </MemoryRouter>
  );
}

function renderMembers(role: Role, canManage: boolean, projectSlug = "shop") {
  mockSession(role);
  return render(membersTree(projectSlug, canManage));
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

  it("announces a failed register load and retries it successfully", async () => {
    const retryStarted = createDeferred<void>();
    const releaseRetry = createDeferred<void>();
    let requests = 0;
    server.use(
      http.get("/api/v1/projects/shop/members", async () => {
        requests += 1;
        if (requests === 1) {
          return apiError(503, "service_unavailable");
        }
        retryStarted.resolve();
        await releaseRetry.promise;
        return HttpResponse.json({ members: [alex] });
      }),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Retry" }));
    await retryStarted.promise;
    expect(screen.getByRole("status", { name: "Member register status" })).toHaveTextContent(
      "Loading project members",
    );
    releaseRetry.resolve();

    expect(await screen.findByText("Alex Smith")).toBeInTheDocument();
    expect(screen.getByRole("status", { name: "Member register status" })).toHaveTextContent(
      "Project members loaded",
    );
  });

  it("discards a late register retry after switching projects", async () => {
    const retryStarted = createDeferred<void>();
    const releaseRetry = createDeferred<void>();
    let shopRequests = 0;
    server.use(
      http.get("/api/v1/projects/shop/members", async () => {
        shopRequests += 1;
        if (shopRequests === 1) {
          return apiError(503, "service_unavailable");
        }
        retryStarted.resolve();
        await releaseRetry.promise;
        return HttpResponse.json({ members: [alex] });
      }),
      http.get("/api/v1/projects/billing/members", () =>
        HttpResponse.json({
          members: [
            {
              user_id: "user-betty",
              username: "betty",
              display_name: "Betty Billing",
              permission: "viewer",
            },
          ],
        }),
      ),
    );
    const view = renderMembers("admin", true);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Retry" }));
    await retryStarted.promise;

    view.rerender(membersTree("billing", true));
    expect(await screen.findByText("Betty Billing")).toBeInTheDocument();
    releaseRetry.resolve();

    await waitFor(() =>
      expect(screen.queryByText("Alex Smith")).not.toBeInTheDocument(),
    );
    expect(screen.getByText("Betty Billing")).toBeInTheDocument();
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
    let currentMembers: TestMember[] = [alex];
    server.use(
      http.get("/api/v1/projects/shop/members", () =>
        HttpResponse.json({ members: currentMembers }),
      ),
      http.put(
        "/api/v1/projects/shop/members/alex.smith",
        async ({ request }) => {
          putPath = new URL(request.url).pathname;
          putBody = await request.json();
          currentMembers = [{ ...alex, permission: "editor" }];
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

  it("serializes save and remove per username while other rows remain independent", async () => {
    const jane = {
      user_id: "user-jane",
      username: "jane.doe",
      display_name: "Jane Doe",
      permission: "viewer" as const,
    };
    let currentMembers: TestMember[] = [alex, jane];
    const alexStarted = createDeferred<void>();
    const releaseAlex = createDeferred<void>();
    const janeStarted = createDeferred<void>();
    const releaseJane = createDeferred<void>();
    server.use(
      http.get("/api/v1/projects/shop/members", () =>
        HttpResponse.json({ members: currentMembers }),
      ),
      http.put("/api/v1/projects/shop/members/:username", async ({ params }) => {
        if (params.username === "alex.smith") {
          alexStarted.resolve();
          await releaseAlex.promise;
        } else {
          janeStarted.resolve();
          await releaseJane.promise;
        }
        currentMembers = currentMembers.map((member) =>
          member.username === params.username
            ? { ...member, permission: "editor" }
            : member,
        );
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    const alexRow = await screen.findByRole("listitem", { name: "Alex Smith access" });
    const janeRow = screen.getByRole("listitem", { name: "Jane Doe access" });

    await user.selectOptions(
      within(alexRow).getByLabelText("Permission for alex.smith"),
      "editor",
    );
    await user.selectOptions(
      within(janeRow).getByLabelText("Permission for jane.doe"),
      "editor",
    );
    await user.click(within(alexRow).getByRole("button", { name: "Save permission" }));
    await alexStarted.promise;

    expect(within(alexRow).getByRole("button", { name: "Saving…" })).toBeDisabled();
    expect(within(alexRow).getByRole("button", { name: "Remove access" })).toBeDisabled();
    expect(within(janeRow).getByRole("button", { name: "Save permission" })).toBeEnabled();

    await user.click(within(janeRow).getByRole("button", { name: "Save permission" }));
    await janeStarted.promise;
    expect(within(alexRow).getByRole("button", { name: "Saving…" })).toBeDisabled();
    expect(within(janeRow).getByRole("button", { name: "Saving…" })).toBeDisabled();

    releaseJane.resolve();
    await waitFor(() =>
      expect(within(janeRow).getByRole("button", { name: "Save permission" })).toBeEnabled(),
    );
    expect(within(alexRow).getByRole("button", { name: "Saving…" })).toBeDisabled();
    releaseAlex.resolve();
    await waitFor(() =>
      expect(within(alexRow).getByRole("button", { name: "Save permission" })).toBeEnabled(),
    );
    expect(within(alexRow).queryByRole("alert")).not.toBeInTheDocument();
    expect(within(janeRow).queryByRole("alert")).not.toBeInTheDocument();
    expect(within(alexRow).getByLabelText("Permission for alex.smith")).toHaveValue(
      "editor",
    );
    expect(within(janeRow).getByLabelText("Permission for jane.doe")).toHaveValue(
      "editor",
    );
  });

  it("applies an older authoritative refresh when the newer concurrent refresh fails", async () => {
    const jane = {
      user_id: "user-jane",
      username: "jane.doe",
      display_name: "Jane Doe",
      permission: "editor" as const,
    };
    let currentMembers: TestMember[] = [alex];
    let gets = 0;
    const olderRefreshStarted = createDeferred<void>();
    const releaseOlderRefresh = createDeferred<void>();
    const newerRefreshFailed = createDeferred<void>();
    server.use(
      http.get("/api/v1/projects/shop/members", async () => {
        gets += 1;
        if (gets === 1) {
          return HttpResponse.json({ members: [alex] });
        }
        if (gets === 2) {
          olderRefreshStarted.resolve();
          await releaseOlderRefresh.promise;
          return HttpResponse.json({ members: currentMembers });
        }
        newerRefreshFailed.resolve();
        return apiError(503, "service_unavailable");
      }),
      http.put("/api/v1/projects/shop/members/jane.doe", () => {
        currentMembers = [...currentMembers, jane];
        return new HttpResponse(null, { status: 204 });
      }),
      http.put("/api/v1/projects/shop/members/alex.smith", () => {
        currentMembers = currentMembers.map((member) =>
          member.username === alex.username
            ? { ...member, permission: "editor" }
            : member,
        );
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    const alexRow = await screen.findByRole("listitem", {
      name: "Alex Smith access",
    });
    const username = screen.getByLabelText("Synchronized username");

    await user.type(username, "jane.doe");
    await user.selectOptions(
      screen.getByLabelText("New member permission"),
      "editor",
    );
    await user.click(screen.getByRole("button", { name: "Add member" }));
    await olderRefreshStarted.promise;

    await user.selectOptions(
      within(alexRow).getByLabelText("Permission for alex.smith"),
      "editor",
    );
    await user.click(
      within(alexRow).getByRole("button", { name: "Save permission" }),
    );
    await newerRefreshFailed.promise;
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "register has not confirmed",
      ),
    );

    releaseOlderRefresh.resolve();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Add member" })).toBeEnabled(),
    );

    expect(screen.getByText("Jane Doe")).toBeInTheDocument();
    expect(username).toHaveValue("");
    expect(screen.getByRole("status")).toHaveTextContent(
      "Member access saved",
    );
    expect(
      within(alexRow).getByLabelText("Permission for alex.smith"),
    ).toHaveValue("editor");
    expect(
      screen.getByRole("button", { name: "Retry register" }),
    ).toBeEnabled();
  });

  it("retains an unconfirmed add and retries the authoritative register", async () => {
    const jane = {
      user_id: "user-jane",
      username: "jane.doe",
      display_name: "Jane Doe",
      permission: "editor" as const,
    };
    let currentMembers: TestMember[] = [alex];
    let gets = 0;
    server.use(
      http.get("/api/v1/projects/shop/members", () => {
        gets += 1;
        return gets === 2
          ? apiError(503, "service_unavailable")
          : HttpResponse.json({ members: currentMembers });
      }),
      http.put("/api/v1/projects/shop/members/jane.doe", () => {
        currentMembers = [...currentMembers, jane];
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    await screen.findByText("Alex Smith");
    const username = screen.getByLabelText("Synchronized username");

    await user.type(username, "jane.doe");
    await user.selectOptions(
      screen.getByLabelText("New member permission"),
      "editor",
    );
    await user.click(screen.getByRole("button", { name: "Add member" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /may have been saved.*register has not confirmed/iu,
    );
    expect(username).toHaveValue("jane.doe");
    expect(screen.getByLabelText("New member permission")).toHaveValue("editor");
    expect(screen.getByRole("status")).not.toHaveTextContent(
      "Member access saved",
    );
    expect(screen.queryByText("SECRET")).not.toBeInTheDocument();

    const retry = screen.getByRole("button", { name: "Retry register" });
    retry.focus();
    expect(retry).toHaveFocus();
    await user.click(retry);

    expect(await screen.findByText("Jane Doe")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(username).toHaveValue("");
    expect(screen.getByRole("status")).toHaveTextContent(
      "Member access saved",
    );
    expect(gets).toBe(3);
  });

  it("retains an unconfirmed permission draft until a register retry applies it", async () => {
    let currentMembers: TestMember[] = [alex];
    let gets = 0;
    server.use(
      http.get("/api/v1/projects/shop/members", () => {
        gets += 1;
        return gets === 2
          ? apiError(503, "service_unavailable")
          : HttpResponse.json({ members: currentMembers });
      }),
      http.put("/api/v1/projects/shop/members/alex.smith", () => {
        currentMembers = [{ ...alex, permission: "editor" }];
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    const row = await screen.findByRole("listitem", { name: "Alex Smith access" });
    const permission = within(row).getByLabelText("Permission for alex.smith");

    await user.selectOptions(permission, "editor");
    await user.click(within(row).getByRole("button", { name: "Save permission" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /may have been saved.*register has not confirmed/iu,
    );
    expect(permission).toHaveValue("editor");
    expect(screen.getByRole("status")).not.toHaveTextContent(
      "updated to Editor",
    );

    await user.click(screen.getByRole("button", { name: "Retry register" }));

    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent(
        "Permission for Alex Smith updated to Editor.",
      ),
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(permission).toHaveValue("editor");
    expect(gets).toBe(3);
  });

  it("reconciles an uncertain permission save from the authoritative register", async () => {
    let currentMembers: TestMember[] = [alex];
    let gets = 0;
    server.use(
      http.get("/api/v1/projects/shop/members", () => {
        gets += 1;
        return HttpResponse.json({ members: currentMembers });
      }),
      http.put("/api/v1/projects/shop/members/alex.smith", () => {
        currentMembers = [{ ...alex, permission: "editor" }];
        return apiError(503, "service_unavailable");
      }),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    const row = await screen.findByRole("listitem", { name: "Alex Smith access" });

    await user.selectOptions(
      within(row).getByLabelText("Permission for alex.smith"),
      "editor",
    );
    await user.click(within(row).getByRole("button", { name: "Save permission" }));

    await waitFor(() => expect(gets).toBe(2));
    expect(within(row).getByLabelText("Permission for alex.smith")).toHaveValue("editor");
    expect(within(row).queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(
      "Permission for Alex Smith updated to Editor.",
    );
  });

  it("discards a deferred save response after switching projects", async () => {
    const saveStarted = createDeferred<void>();
    const releaseSave = createDeferred<void>();
    server.use(
      http.get("/api/v1/projects/shop/members", () =>
        HttpResponse.json({ members: [alex] }),
      ),
      http.get("/api/v1/projects/billing/members", () =>
        HttpResponse.json({
          members: [
            {
              user_id: "user-betty",
              username: "betty",
              display_name: "Betty Billing",
              permission: "viewer",
            },
          ],
        }),
      ),
      http.put("/api/v1/projects/shop/members/alex.smith", async () => {
        saveStarted.resolve();
        await releaseSave.promise;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const view = renderMembers("admin", true);
    const user = userEvent.setup();
    const row = await screen.findByRole("listitem", { name: "Alex Smith access" });
    await user.selectOptions(
      within(row).getByLabelText("Permission for alex.smith"),
      "editor",
    );
    await user.click(within(row).getByRole("button", { name: "Save permission" }));
    await saveStarted.promise;

    view.rerender(membersTree("billing", true));
    expect(await screen.findByText("Betty Billing")).toBeInTheDocument();
    releaseSave.resolve();

    await waitFor(() =>
      expect(screen.getByRole("status")).not.toHaveTextContent("Alex Smith"),
    );
    expect(screen.getByText("Betty Billing")).toBeInTheDocument();
  });

  it("requires named confirmation, preserves it on failure, and retries removal", async () => {
    let removeRequests = 0;
    let currentMembers: TestMember[] = [alex];
    server.use(
      http.get("/api/v1/projects/shop/members", () =>
        HttpResponse.json({ members: currentMembers }),
      ),
      http.delete("/api/v1/projects/shop/members/alex.smith", () => {
        removeRequests += 1;
        if (removeRequests === 1) {
          return apiError(503, "service_unavailable");
        }
        currentMembers = [];
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    const row = await screen.findByRole("listitem", { name: "Alex Smith access" });

    const removeButton = within(row).getByRole("button", { name: "Remove access" });
    await user.click(removeButton);
    let dialog = screen.getByRole("dialog", { name: "Remove Alex Smith access" });
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus();
    expect(within(dialog).getByText(/Alex Smith \(@alex\.smith\)/u)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(removeButton).toHaveFocus();
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
    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveFocus(),
    );
  });

  it("reconciles an uncertain removal from the authoritative register", async () => {
    let currentMembers: TestMember[] = [alex];
    let gets = 0;
    server.use(
      http.get("/api/v1/projects/shop/members", () => {
        gets += 1;
        return HttpResponse.json({ members: currentMembers });
      }),
      http.delete("/api/v1/projects/shop/members/alex.smith", () => {
        currentMembers = [];
        return apiError(503, "service_unavailable");
      }),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    const row = await screen.findByRole("listitem", { name: "Alex Smith access" });
    await user.click(within(row).getByRole("button", { name: "Remove access" }));
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Remove access" }),
    );

    await waitFor(() => expect(gets).toBe(2));
    expect(screen.queryByText("Alex Smith")).not.toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(
      "Access removed for Alex Smith.",
    );
  });

  it("keeps removal confirmation reachable until a register retry applies it", async () => {
    let currentMembers: TestMember[] = [alex];
    let gets = 0;
    server.use(
      http.get("/api/v1/projects/shop/members", () => {
        gets += 1;
        return gets === 2
          ? apiError(503, "service_unavailable")
          : HttpResponse.json({ members: currentMembers });
      }),
      http.delete("/api/v1/projects/shop/members/alex.smith", () => {
        currentMembers = [];
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderMembers("admin", true);
    const user = userEvent.setup();
    const row = await screen.findByRole("listitem", { name: "Alex Smith access" });
    await user.click(within(row).getByRole("button", { name: "Remove access" }));
    const dialog = screen.getByRole("dialog", { name: "Remove Alex Smith access" });
    await user.click(within(dialog).getByRole("button", { name: "Remove access" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      /may have been saved.*register has not confirmed/iu,
    );
    expect(screen.getAllByRole("alert")).toHaveLength(1);
    expect(
      within(dialog).getByRole("button", { name: "Remove access" }),
    ).toBeDisabled();
    const retry = within(dialog).getByRole("button", { name: "Retry register" });
    retry.focus();
    expect(retry).toHaveFocus();
    await user.click(retry);

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.queryByText("Alex Smith")).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(
      "Access removed for Alex Smith.",
    );
    expect(gets).toBe(3);
  });

  it("discards a deferred removal response after switching projects", async () => {
    const removeStarted = createDeferred<void>();
    const releaseRemove = createDeferred<void>();
    server.use(
      http.get("/api/v1/projects/shop/members", () =>
        HttpResponse.json({ members: [alex] }),
      ),
      http.get("/api/v1/projects/billing/members", () =>
        HttpResponse.json({
          members: [
            {
              user_id: "user-betty",
              username: "betty",
              display_name: "Betty Billing",
              permission: "viewer",
            },
          ],
        }),
      ),
      http.delete("/api/v1/projects/shop/members/alex.smith", async () => {
        removeStarted.resolve();
        await releaseRemove.promise;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const view = renderMembers("admin", true);
    const user = userEvent.setup();
    const row = await screen.findByRole("listitem", { name: "Alex Smith access" });
    await user.click(within(row).getByRole("button", { name: "Remove access" }));
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Remove access" }),
    );
    await removeStarted.promise;

    view.rerender(membersTree("billing", true));
    expect(await screen.findByText("Betty Billing")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    releaseRemove.resolve();

    await waitFor(() =>
      expect(screen.getByRole("status")).not.toHaveTextContent("Alex Smith"),
    );
    expect(screen.getByText("Betty Billing")).toBeInTheDocument();
  });
});
