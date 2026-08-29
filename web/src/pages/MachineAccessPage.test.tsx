import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { delay, http, HttpResponse } from "msw";
import { describe, expect, it, vi } from "vitest";
import { App } from "../app/App";
import { APIClient, APIError } from "../api/client";
import { server } from "../test/setup";
import { IssueTokenDialog } from "./MachineAccessPage";

const identity = {
  id: "machine-ci",
  name: "shop-ci",
  description: "Production delivery identity",
  enabled: true,
  created_at: "2026-08-20T08:00:00Z",
  updated_at: "2026-08-29T08:30:00Z",
};

const primaryToken = {
  id: "token-primary",
  name: "primary",
  prefix: "ch_abc1234",
  created_at: "2026-08-29T08:30:00Z",
  expires_at: "2026-10-29T08:30:00Z",
  revoked_at: null,
};

function mockSession(role: "admin" | "member") {
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

function mockMachinePage({ tokens = [primaryToken] } = {}) {
  server.use(
    http.get("/api/v1/machine-identities", () =>
      HttpResponse.json({ identities: [identity] }),
    ),
    http.get("/api/v1/machine-identities/machine-ci", () =>
      HttpResponse.json({ identity: { ...identity, grants: [], tokens } }),
    ),
    http.get("/api/v1/projects", () =>
      HttpResponse.json({
        projects: [
          {
            id: "project-shop",
            slug: "shop",
            name: "Shop 商店",
            description: "Storefront",
            created_at: "2026-08-20T08:00:00Z",
            updated_at: "2026-08-29T08:00:00Z",
          },
        ],
      }),
    ),
    http.get("/api/v1/projects/shop", () =>
      HttpResponse.json({
        project: {
          id: "project-shop",
          slug: "shop",
          name: "Shop 商店",
          description: "Storefront",
          permission: "admin",
          environments: [
            {
              id: "environment-prod",
              project_id: "project-shop",
              slug: "prod",
              name: "Production 🚀",
              current_revision_id: null,
              created_at: "2026-08-20T08:00:00Z",
              updated_at: "2026-08-29T08:00:00Z",
            },
          ],
          created_at: "2026-08-20T08:00:00Z",
          updated_at: "2026-08-29T08:00:00Z",
        },
      }),
    ),
  );
}

function renderAdminAt(path = "/machine-access") {
  mockSession("admin");
  window.history.pushState({}, "", path);
  return render(<App />);
}

function apiError(status: number, code: string, fields: Record<string, string> = {}) {
  return HttpResponse.json(
    {
      error: {
        code,
        message: "remote SECRET detail",
        request_id: "req-machine",
        fields,
      },
    },
    { status },
  );
}

describe("MachineAccessPage", () => {
  it("redirects a non-admin before loading machine administration", async () => {
    mockSession("member");
    let machineRequests = 0;
    server.use(
      http.get("/api/v1/projects", () => HttpResponse.json({ projects: [] })),
      http.get("/api/v1/machine-identities", () => {
        machineRequests += 1;
        return HttpResponse.json({ identities: [] });
      }),
    );
    window.history.pushState({}, "", "/machine-access");
    render(<App />);

    expect(await screen.findByRole("heading", { name: "Projects" })).toBeInTheDocument();
    expect(window.location.pathname).toBe("/projects");
    expect(machineRequests).toBe(0);
  });

  it("submits explicit project and environment grants by their existing IDs", async () => {
    mockMachinePage({ tokens: [] });
    let requestBody: unknown;
    server.use(
      http.put("/api/v1/machine-identities/machine-ci/grants", async ({ request }) => {
        requestBody = await request.json();
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderAdminAt();
    const user = userEvent.setup();

    expect(await screen.findByRole("heading", { name: "shop-ci" })).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Project"), "project-shop");
    await user.selectOptions(screen.getByLabelText("Environment"), "environment-prod");
    await user.click(screen.getByRole("button", { name: "Add grant" }));
    expect(screen.getByText("Shop 商店 / Production 🚀")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save grants" }));

    await waitFor(() =>
      expect(requestBody).toEqual({
        grants: [
          { project_id: "project-shop", environment_id: "environment-prod" },
        ],
      }),
    );
    expect(screen.getByRole("status")).toHaveTextContent("Grants saved");
  });

  it("creates a named identity and opens its durable register entry", async () => {
    mockMachinePage({ tokens: [] });
    const created = {
      ...identity,
      id: "machine-stage",
      name: "stage-ci",
      description: "Staging delivery",
    };
    let requestBody: unknown;
    server.use(
      http.post("/api/v1/machine-identities", async ({ request }) => {
        requestBody = await request.json();
        return HttpResponse.json({ identity: created }, { status: 201 });
      }),
      http.get("/api/v1/machine-identities/machine-stage", () =>
        HttpResponse.json({ identity: { ...created, grants: [], tokens: [] } }),
      ),
    );
    renderAdminAt();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "New identity" }));
    const dialog = screen.getByRole("dialog", { name: "New machine identity" });
    await user.type(within(dialog).getByLabelText("Machine name"), "stage-ci");
    await user.type(within(dialog).getByLabelText("Description"), "Staging delivery");
    await user.click(within(dialog).getByRole("button", { name: "Create identity" }));

    expect(await screen.findByRole("heading", { name: "stage-ci" })).toBeInTheDocument();
    expect(requestBody).toEqual({ name: "stage-ci", description: "Staging delivery", enabled: true });
  });

  it("enforces identity UTF-8 byte boundaries without rejecting exact-limit CJK values", async () => {
    mockMachinePage({ tokens: [] });
    const exactName = `${"界".repeat(42)}ab`;
    const exactDescription = `${"界".repeat(340)}abcd`;
    const submittedName = `\u0085${exactName}\u0085`;
    const submittedDescription = `\u0085${exactDescription}\u0085`;
    const created = { ...identity, id: "machine-boundary", name: exactName, description: exactDescription };
    let requests = 0;
    let requestBody: unknown;
    server.use(
      http.post("/api/v1/machine-identities", async ({ request }) => {
        requests += 1;
        requestBody = await request.json();
        return HttpResponse.json({ identity: created }, { status: 201 });
      }),
      http.get("/api/v1/machine-identities/machine-boundary", () =>
        HttpResponse.json({ identity: { ...created, grants: [], tokens: [] } }),
      ),
    );
    renderAdminAt();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "New identity" }));
    let dialog = screen.getByRole("dialog", { name: "New machine identity" });
    fireEvent.change(within(dialog).getByLabelText("Machine name"), { target: { value: submittedName } });
    fireEvent.change(within(dialog).getByLabelText("Description"), { target: { value: submittedDescription } });
    expect(within(dialog).getByLabelText("Machine name")).toHaveAccessibleDescription(/128 bytes.*limit: 128 bytes/iu);
    expect(within(dialog).getByLabelText("Description")).toHaveAccessibleDescription(/1024 bytes.*limit: 1024 bytes/iu);
    await user.click(within(dialog).getByRole("button", { name: "Create identity" }));

    await waitFor(() => expect(requests).toBe(1));
    expect(requestBody).toEqual({ name: submittedName, description: submittedDescription, enabled: true });

    await user.click(screen.getByRole("button", { name: "New identity" }));
    dialog = screen.getByRole("dialog", { name: "New machine identity" });
    const name = within(dialog).getByLabelText("Machine name");
    const description = within(dialog).getByLabelText("Description");
    fireEvent.change(name, { target: { value: "界".repeat(43) } });
    fireEvent.change(description, { target: { value: "界".repeat(342) } });
    await user.click(within(dialog).getByRole("button", { name: "Create identity" }));

    expect(requests).toBe(1);
    expect(name).toHaveValue("界".repeat(43));
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAttribute("aria-describedby", "machine-name-help machine-name-error");
    expect(description).toHaveAttribute("aria-invalid", "true");
    expect(description).toHaveAttribute("aria-describedby", "machine-description-help machine-description-error");
  });

  it("maps create identity name and description field errors without losing input", async () => {
    mockMachinePage({ tokens: [] });
    server.use(
      http.post("/api/v1/machine-identities", () => apiError(422, "validation_failed", {
        name: "The service rejected this machine name.",
        description: "The service rejected this description.",
      })),
    );
    renderAdminAt();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "New identity" }));
    const dialog = screen.getByRole("dialog", { name: "New machine identity" });
    const name = within(dialog).getByLabelText("Machine name");
    const description = within(dialog).getByLabelText("Description");
    await user.type(name, "build-ci");
    await user.type(description, "retained description");
    await user.click(within(dialog).getByRole("button", { name: "Create identity" }));

    expect(await within(dialog).findByText("The service rejected this machine name.")).toBeInTheDocument();
    expect(name).toHaveValue("build-ci");
    expect(description).toHaveValue("retained description");
    expect(name).toHaveAttribute("aria-describedby", "machine-name-help machine-name-error");
    expect(description).toHaveAttribute("aria-describedby", "machine-description-help machine-description-error");
    expect(screen.queryByText("remote SECRET detail")).not.toBeInTheDocument();
  });

  it("does not offer an invalid grant when a project has no environments", async () => {
    mockMachinePage({ tokens: [] });
    server.use(
      http.get("/api/v1/projects/shop", () =>
        HttpResponse.json({
          project: {
            id: "project-shop",
            slug: "shop",
            name: "Shop 商店",
            description: "Storefront",
            permission: "admin",
            environments: [],
            created_at: "2026-08-20T08:00:00Z",
            updated_at: "2026-08-29T08:00:00Z",
          },
        }),
      ),
    );
    renderAdminAt();

    const environment = await screen.findByLabelText("Environment");
    expect(environment).toBeDisabled();
    expect(screen.getByRole("button", { name: "Add grant" })).toBeDisabled();
  });

  it("retains grant selections and bounds a failed save response", async () => {
    mockMachinePage({ tokens: [] });
    server.use(
      http.put("/api/v1/machine-identities/machine-ci/grants", () => apiError(500, "internal_error")),
    );
    renderAdminAt();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Add grant" }));
    await user.click(screen.getByRole("button", { name: "Save grants" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/couldn’t be saved/iu);
    expect(screen.getByText("Shop 商店 / Production 🚀")).toBeInTheDocument();
    expect(screen.queryByText("remote SECRET detail")).not.toBeInTheDocument();
  });

  it("never reveals an issued Token after the one-time dialog closes", async () => {
    mockMachinePage({ tokens: [] });
    server.use(
      http.post("/api/v1/machine-identities/machine-ci/tokens", async () => {
        await delay(20);
        return HttpResponse.json(
          { token: { ...primaryToken, plaintext: "ch_once_only" } },
          { status: 201 },
        );
      }),
    );
    renderAdminAt();
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);

    await user.click(await screen.findByRole("button", { name: "Issue Token" }));
    const issueDialog = screen.getByRole("dialog", { name: "Issue Token" });
    await user.type(within(issueDialog).getByLabelText("Token name"), "primary");
    await user.click(within(issueDialog).getByRole("button", { name: "Issue Token" }));
    expect(await screen.findByText("ch_once_only")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Copy Token" }));
    expect(writeText).toHaveBeenCalledWith("ch_once_only");
    await user.click(screen.getByRole("button", { name: "I have copied it" }));
    expect(screen.queryByText("ch_once_only")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "View primary Token" }));
    expect(screen.queryByText("ch_once_only")).not.toBeInTheDocument();
    expect(within(screen.getByRole("dialog", { name: "primary Token" })).getByText("ch_abc1234")).toBeInTheDocument();
    writeText.mockRestore();
  });

  it("passes only metadata beyond the one-time Token dialog", async () => {
    const client = new APIClient(() => "csrf-admin");
    vi.spyOn(client, "post").mockResolvedValue({
      token: { ...primaryToken, plaintext: "ch_dialog_boundary" },
    });
    const onIssued = vi.fn();
    render(
      <IssueTokenDialog
        client={client}
        identityID="machine-ci"
        onClose={vi.fn()}
        onIssued={onIssued}
      />,
    );
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("Token name"), "primary");
    await user.click(screen.getByRole("button", { name: "Issue Token" }));

    expect(await screen.findByText("ch_dialog_boundary")).toBeInTheDocument();
    expect(onIssued).toHaveBeenCalledWith(primaryToken);
    expect(onIssued.mock.calls[0][0]).not.toHaveProperty("plaintext");
  });

  it("validates Token UTF-8 bytes and expiry, then associates server field errors", async () => {
    const client = new APIClient(() => "csrf-admin");
    const post = vi.spyOn(client, "post").mockRejectedValue(
      new APIError(422, "validation_failed", "remote SECRET detail", "req-token", {
        name: "The service rejected this Token name.",
        expires_at: "The service rejected this expiry.",
      }),
    );
    render(
      <IssueTokenDialog
        client={client}
        identityID="machine-ci"
        onClose={vi.fn()}
        onIssued={vi.fn()}
      />,
    );
    const user = userEvent.setup();
    const name = screen.getByLabelText("Token name");
    const expiry = screen.getByLabelText("Expires at");

    fireEvent.change(expiry, { target: { value: "2000-01-01T00:00" } });
    await user.click(screen.getByRole("button", { name: "Issue Token" }));
    expect(post).not.toHaveBeenCalled();
    expect(expiry).toHaveAttribute("aria-invalid", "true");
    expect(expiry).toHaveAttribute("aria-describedby", "token-expiry-error");

    fireEvent.change(expiry, { target: { value: new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString().slice(0, 16) } });
    fireEvent.change(name, { target: { value: "界".repeat(43) } });
    await user.click(screen.getByRole("button", { name: "Issue Token" }));
    expect(post).not.toHaveBeenCalled();
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAttribute("aria-describedby", "token-name-help token-name-error");

    const exactName = `${"界".repeat(42)}ab`;
    fireEvent.change(name, { target: { value: exactName } });
    expect(name).toHaveAccessibleDescription(/128 bytes.*limit: 128 bytes/iu);
    await user.click(screen.getByRole("button", { name: "Issue Token" }));

    expect(await screen.findByText("The service rejected this Token name.")).toBeInTheDocument();
    expect(name).toHaveValue(exactName);
    expect(name).toHaveAttribute("aria-describedby", "token-name-help token-name-error");
    expect(expiry).toHaveAttribute("aria-describedby", "token-expiry-error");
    expect(screen.queryByText("remote SECRET detail")).not.toBeInTheDocument();
  });

  it("keeps the only plaintext copy when clipboard access fails", async () => {
    mockMachinePage({ tokens: [] });
    server.use(
      http.post("/api/v1/machine-identities/machine-ci/tokens", () =>
        HttpResponse.json(
          { token: { ...primaryToken, plaintext: "ch_copy_retry" } },
          { status: 201 },
        ),
      ),
    );
    renderAdminAt();
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("denied"));
    await user.click(await screen.findByRole("button", { name: "Issue Token" }));
    const dialog = screen.getByRole("dialog", { name: "Issue Token" });
    await user.type(within(dialog).getByLabelText("Token name"), "primary");
    await user.click(within(dialog).getByRole("button", { name: "Issue Token" }));
    await user.click(await screen.findByRole("button", { name: "Copy Token" }));

    expect(screen.getByText("ch_copy_retry")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(/copy failed/iu);
    writeText.mockRestore();
  });

  it("confirms revocation, restores focus on Escape, and prevents duplicate submits", async () => {
    mockMachinePage();
    let requests = 0;
    server.use(
      http.delete("/api/v1/machine-identities/machine-ci/tokens/token-primary", async () => {
        requests += 1;
        await delay(30);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderAdminAt();
    const user = userEvent.setup();

    const revoke = await screen.findByRole("button", { name: "Revoke primary Token" });
    await user.click(revoke);
    expect(screen.getByRole("dialog", { name: "Revoke primary Token?" })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(revoke).toHaveFocus();

    await user.click(revoke);
    const dialog = screen.getByRole("dialog", { name: "Revoke primary Token?" });
    await user.dblClick(within(dialog).getByRole("button", { name: "Revoke Token" }));
    expect(within(dialog).getByRole("button", { name: "Revoking…" })).toBeDisabled();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(requests).toBe(1);
    expect(screen.getByText("Revoked")).toBeInTheDocument();
  });

  it("keeps revocation confirmation open when the server rejects it", async () => {
    mockMachinePage();
    server.use(
      http.delete("/api/v1/machine-identities/machine-ci/tokens/token-primary", () => apiError(503, "unavailable")),
    );
    renderAdminAt();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Revoke primary Token" }));
    const dialog = screen.getByRole("dialog", { name: "Revoke primary Token?" });
    await user.click(within(dialog).getByRole("button", { name: "Revoke Token" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(/couldn’t be revoked/iu);
    expect(dialog).toBeInTheDocument();
    expect(screen.getByText("Active")).toBeInTheDocument();
    expect(screen.queryByText("remote SECRET detail")).not.toBeInTheDocument();
  });

  it("retains editable identity state and bounds server failures", async () => {
    mockMachinePage({ tokens: [] });
    server.use(
      http.put("/api/v1/machine-identities/machine-ci", () => apiError(500, "internal_error")),
    );
    renderAdminAt();
    const user = userEvent.setup();
    const description = await screen.findByLabelText("Description");
    await user.clear(description);
    await user.type(description, "部署身份 🚀");
    await user.click(screen.getByRole("button", { name: "Save identity" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/couldn’t save/iu);
    expect(description).toHaveValue("部署身份 🚀");
    expect(screen.queryByText("remote SECRET detail")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Machine name")).not.toBeInTheDocument();
  });

  it("validates edited description bytes and associates the server description error", async () => {
    mockMachinePage({ tokens: [] });
    let requests = 0;
    server.use(
      http.put("/api/v1/machine-identities/machine-ci", () => {
        requests += 1;
        return apiError(422, "validation_failed", {
          description: "The service rejected this edited description.",
        });
      }),
    );
    renderAdminAt();
    const user = userEvent.setup();
    const description = await screen.findByLabelText("Description");

    fireEvent.change(description, { target: { value: "界".repeat(342) } });
    await user.click(screen.getByRole("button", { name: "Save identity" }));
    expect(requests).toBe(0);
    expect(description).toHaveAttribute("aria-invalid", "true");
    expect(description).toHaveAttribute(
      "aria-describedby",
      "machine-description-machine-ci-help machine-description-machine-ci-error",
    );

    const exactDescription = `${"界".repeat(340)}abcd`;
    fireEvent.change(description, { target: { value: exactDescription } });
    await user.click(screen.getByRole("button", { name: "Save identity" }));

    expect(await screen.findByText("The service rejected this edited description.")).toBeInTheDocument();
    expect(requests).toBe(1);
    expect(description).toHaveValue(exactDescription);
    expect(description).toHaveAccessibleDescription(/1024 bytes.*The service rejected this edited description/iu);
    expect(screen.queryByText("remote SECRET detail")).not.toBeInTheDocument();
  });
});
