import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, RouterProvider, createMemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { APIError } from "../../api/client";
import type { APIClientContract, Revision } from "../../api/types";
import { ConfigEditor } from "./ConfigEditor";

const revision: Revision = {
  id: "revision-7",
  environment_id: "env-prod",
  message: "current",
  created_by: "user-admin",
  version: 7,
  created_at: "2026-08-29T08:00:00Z",
  entries: [{ key: "EMPTY", value: "", service: "api" }],
};

function client(): APIClientContract {
  return {
    get: vi.fn(),
    post: vi.fn(),
    postNoContent: vi.fn(),
    put: vi.fn(),
    putNoContent: vi.fn(),
    delete: vi.fn(),
  };
}

function renderEditor(
  api: APIClientContract = client(),
  current = revision,
  initialEntries: string[] = ["/edit"],
) {
  const onCancel = vi.fn();
  const onSaved = vi.fn();
  const router = createMemoryRouter(
    [
      {
        path: "/edit",
        element: (
          <>
            <Link to="/projects">Project register</Link>
            <ConfigEditor
              client={api}
              projectSlug="shop"
              environmentSlug="prod"
              revision={current}
              onCancel={onCancel}
              onSaved={onSaved}
            />
          </>
        ),
      },
      { path: "/projects", element: <h1>Projects destination</h1> },
    ],
    { initialEntries, initialIndex: initialEntries.length - 1 },
  );
  render(<RouterProvider router={router} />);
  return { api, onCancel, onSaved, router };
}

describe("ConfigEditor", () => {
  it("starts from the exact loaded snapshot", () => {
    renderEditor();

    expect(screen.getByLabelText("Value for EMPTY")).toHaveValue("");
    expect(screen.getByLabelText("Service for EMPTY")).toHaveValue("api");
  });

  it("adds, edits, and deletes rows then sends one complete trimmed snapshot", async () => {
    const api = client();
    vi.mocked(api.put).mockResolvedValue({ revision: { ...revision, version: 8 } });
    renderEditor(api, {
      ...revision,
      entries: [
        { key: "FIRST", value: "one", service: "api" },
        { key: "SECOND", value: "two", service: "worker" },
      ],
    });
    const user = userEvent.setup();

    await user.clear(screen.getByLabelText("Value for FIRST"));
    await user.type(screen.getByLabelText("Value for FIRST"), "  exact 😀\nline  ");
    await user.click(screen.getByRole("button", { name: "Delete SECOND" }));
    await user.click(screen.getByRole("button", { name: "Add entry" }));
    await user.type(screen.getByLabelText("Key for new entry"), " NEW_KEY ");
    await user.type(screen.getByLabelText("Value for NEW_KEY"), "值 ");
    await user.type(screen.getByLabelText("Service for NEW_KEY"), " worker ");
    await user.type(screen.getByLabelText("Change message"), "ship exact values");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1));
    expect(api.put).toHaveBeenCalledWith(
      "/projects/shop/environments/prod/config",
      {
        base_revision: 7,
        message: "ship exact values",
        entries: [
          { key: "FIRST", value: "  exact 😀\nline  ", service: "api" },
          { key: "NEW_KEY", value: "值 ", service: "worker" },
        ],
      },
    );
  });

  it("associates invalid and trimmed duplicate key errors with their rows", async () => {
    const api = client();
    renderEditor(api, {
      ...revision,
      entries: [
        { key: "VALID", value: "one", service: "" },
        { key: "SECOND", value: "two", service: "" },
      ],
    });
    const user = userEvent.setup();
    const first = screen.getByLabelText("Key for VALID");
    const second = screen.getByLabelText("Key for SECOND");
    await user.clear(first);
    await user.type(first, "not-valid");
    await user.clear(second);
    await user.type(second, " not-valid ");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    expect(first).toHaveAttribute("aria-invalid", "true");
    expect(first).toHaveAccessibleDescription(/letters, numbers, and underscores/u);
    expect(second).toHaveAccessibleDescription(/Each key must be unique/u);
    expect(api.put).not.toHaveBeenCalled();
  });

  it("maps 422 entry fields back to stable draft rows and retains all input", async () => {
    const api = client();
    vi.mocked(api.put).mockRejectedValue(
      new APIError(422, "validation_failed", "SECRET", "req", {
        "entries[1].value": "Value is too long.",
        "entries[1].service": "Service is too long.",
        message: "Message is required.",
        entries: "Review the entries.",
      }),
    );
    renderEditor(api, {
      ...revision,
      entries: [
        { key: "FIRST", value: "one", service: "api" },
        { key: "SECOND", value: "two", service: "worker" },
      ],
    });
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Value for SECOND"), " retained");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    expect(await screen.findByLabelText("Value for SECOND")).toHaveValue("two retained");
    expect(screen.getByLabelText("Value for SECOND")).toHaveAccessibleDescription("Value is too long.");
    expect(screen.getByLabelText("Service for SECOND")).toHaveAccessibleDescription("Service is too long.");
    expect(screen.getByLabelText("Change message")).toHaveAccessibleDescription("Message is required.");
    expect(screen.getByRole("alert")).toHaveTextContent("Review the marked fields");
    expect(screen.queryByText("SECRET")).not.toBeInTheDocument();
  });

  it("shows only expected top-level 422 fields inline and keeps the exact draft", async () => {
    const api = client();
    vi.mocked(api.put).mockRejectedValue(
      new APIError(422, "validation_failed", "RAW ENVELOPE SECRET", "req", {
        entries: "Combined entry content must be at most 1 byte.",
        message: "Change message must be shorter.",
        unexpected: "UNKNOWN FIELD SECRET",
      }),
    );
    renderEditor(api);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Value for EMPTY"), " exact retained value ");
    await user.type(screen.getByLabelText("Change message"), "retained message");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    const entriesEditor = await screen.findByRole("group", { name: "Configuration entries" });
    expect(within(entriesEditor).getByText("Combined entry content must be at most 1 byte.")).toBeInTheDocument();
    expect(entriesEditor).toHaveAccessibleDescription("Combined entry content must be at most 1 byte.");
    expect(screen.getByLabelText("Value for EMPTY")).toHaveValue(" exact retained value ");
    expect(screen.getByLabelText("Change message")).toHaveValue("retained message");
    expect(screen.getByLabelText("Change message")).toHaveAccessibleDescription("Change message must be shorter.");
    expect(screen.queryByText("RAW ENVELOPE SECRET")).not.toBeInTheDocument();
    expect(screen.queryByText("UNKNOWN FIELD SECRET")).not.toBeInTheDocument();
  });

  it("installs dirty unload and router guards and supports Stay or Discard and leave", async () => {
    const { onCancel } = renderEditor();
    const user = userEvent.setup();
    const cleanEvent = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(cleanEvent);
    expect(cleanEvent.defaultPrevented).toBe(false);

    await user.type(screen.getByLabelText("Value for EMPTY"), "draft");
    const dirtyEvent = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(dirtyEvent);
    expect(dirtyEvent.defaultPrevented).toBe(true);
    await user.click(screen.getByRole("link", { name: "Project register" }));
    expect(screen.getByRole("dialog", { name: "Leave without saving?" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Stay" }));
    expect(screen.getByLabelText("Value for EMPTY")).toHaveValue("draft");
    expect(screen.queryByRole("heading", { name: "Projects destination" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Cancel editing" }));
    expect(screen.getByRole("dialog", { name: "Leave without saving?" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Stay" }));
    expect(onCancel).not.toHaveBeenCalled();
    await user.click(screen.getByRole("link", { name: "Project register" }));
    await user.click(screen.getByRole("button", { name: "Discard and leave" }));
    expect(await screen.findByRole("heading", { name: "Projects destination" })).toBeInTheDocument();
  });

  it("blocks browser-history back navigation until the draft is explicitly discarded", async () => {
    const { router } = renderEditor(client(), revision, ["/projects", "/edit"]);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Value for EMPTY"), "history draft");

    await router.navigate(-1);
    expect(await screen.findByRole("dialog", { name: "Leave without saving?" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Stay" }));
    expect(router.state.location.pathname).toBe("/edit");
    expect(screen.getByLabelText("Value for EMPTY")).toHaveValue("history draft");

    await router.navigate(-1);
    await user.click(await screen.findByRole("button", { name: "Discard and leave" }));
    expect(await screen.findByRole("heading", { name: "Projects destination" })).toBeInTheDocument();
  });

  it("retains a conflicted draft, compares latest values, and requires adopting the new base", async () => {
    const api = client();
    vi.mocked(api.put)
      .mockRejectedValueOnce(new APIError(409, "revision_conflict", "SECRET", "req", {}))
      .mockResolvedValueOnce({ revision: { ...revision, id: "revision-9", version: 9 } });
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        ...revision,
        id: "revision-8",
        version: 8,
        entries: [
          { key: "EMPTY", value: "server latest", service: "server" },
          { key: "SERVER_ONLY", value: "", service: "" },
        ],
      },
    });
    renderEditor(api);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Value for EMPTY"), "local draft  ");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Configuration changed since you loaded it",
    );
    expect(screen.getByLabelText("Value for EMPTY")).toHaveValue("local draft  ");
    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Refresh and compare" }));
    expect(await screen.findByRole("heading", { name: "Latest server compared with your draft" })).toBeInTheDocument();
    expect(screen.getByTestId("conflict-server-EMPTY").textContent).toBe("server latest");
    expect(screen.getByTestId("conflict-local-EMPTY").textContent).toBe("local draft  ");
    expect(screen.getByText("Absent", { selector: "span" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Use version 8 as new base" }));
    expect(screen.getByRole("button", { name: "Save changes" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2));
    expect(vi.mocked(api.put).mock.calls[1]?.[1]).toMatchObject({ base_revision: 8 });
  });

  it("prevents double submit and keeps pending saves non-cancelable", async () => {
    let resolveSave!: (value: unknown) => void;
    const api = client();
    vi.mocked(api.put).mockReturnValue(new Promise<never>((resolve) => {
      resolveSave = resolve as (value: unknown) => void;
    }));
    const { onSaved } = renderEditor(api);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Value for EMPTY"), "pending");
    const save = screen.getByRole("button", { name: "Save changes" });
    await user.dblClick(save);

    expect(api.put).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Saving…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel editing" })).toBeDisabled();
    resolveSave({ revision: { ...revision, version: 8 } });
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
  });
});
