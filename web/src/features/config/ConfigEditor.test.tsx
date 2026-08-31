import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, RouterProvider, createMemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { APIError } from "../../api/client";
import type { APIClientContract, Revision } from "../../api/types";
import { changeLocale } from "../../i18n";
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

function dispatchTextareaEdit(
  textarea: HTMLTextAreaElement,
  {
    data,
    inputType,
    nextDisplayValue,
    nextSelection,
    selectionEnd,
    selectionStart,
  }: {
    data: string | null;
    inputType: string;
    nextDisplayValue: string;
    nextSelection: number;
    selectionStart: number;
    selectionEnd: number;
  },
) {
  textarea.focus();
  textarea.setSelectionRange(selectionStart, selectionEnd);
  fireEvent(textarea, new InputEvent("beforeinput", {
    bubbles: true,
    cancelable: true,
    data,
    inputType,
  }));
  const valueSetter = Object.getOwnPropertyDescriptor(
    HTMLTextAreaElement.prototype,
    "value",
  )?.set;
  if (valueSetter === undefined) {
    throw new Error("Expected the native textarea value setter.");
  }
  valueSetter.call(textarea, nextDisplayValue);
  textarea.setSelectionRange(nextSelection, nextSelection);
  fireEvent(textarea, new InputEvent("input", {
    bubbles: true,
    data,
    inputType,
  }));
}

describe("ConfigEditor", () => {
  it("starts from the exact loaded snapshot", () => {
    renderEditor();

    expect(screen.getByLabelText("Value for EMPTY")).toHaveValue("");
    expect(screen.getByLabelText<HTMLTextAreaElement>("Value for EMPTY").style.resize).toBe("none");
    expect(screen.getByLabelText("Service for EMPTY")).toHaveValue("api");
  });

  it("keeps the dirty editor tree, focus, selection, and leave protection when the locale changes", async () => {
    renderEditor(client(), {
      ...revision,
      entries: [{ key: "DATABASE_URL", value: "postgres", service: "api" }],
    });
    const user = userEvent.setup();
    const value = screen.getByLabelText<HTMLTextAreaElement>("Value for DATABASE_URL");
    await user.type(value, "-draft");
    value.setSelectionRange(3, 3);

    await act(async () => {
      await changeLocale("zh-CN");
    });

    const localizedValue = await screen.findByLabelText<HTMLTextAreaElement>("DATABASE_URL 的值");
    expect(localizedValue).toBe(value);
    expect(localizedValue).toHaveValue("postgres-draft");
    expect(localizedValue).toHaveFocus();
    expect(localizedValue.selectionStart).toBe(3);
    expect(localizedValue.selectionEnd).toBe(3);
    const dirtyEvent = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(dirtyEvent);
    expect(dirtyEvent.defaultPrevented).toBe(true);

    await user.click(screen.getByRole("button", { name: "取消编辑" }));
    expect(screen.getByRole("dialog", { name: "不保存就离开？" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "继续编辑" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "放弃并离开" })).toBeInTheDocument();
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
    await user.click(screen.getByRole("button", { name: "Remove SECOND" }));
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

  it("uses the selected adjacent newline when submitting mixed textarea edits", async () => {
    const api = client();
    vi.mocked(api.put).mockResolvedValue({ revision: { ...revision, version: 8 } });
    renderEditor(api, {
      ...revision,
      entries: [{ key: "MIXED", value: "A\rB\r\r\nC\nD", service: "api" }],
    });
    const user = userEvent.setup();
    const value = screen.getByLabelText<HTMLTextAreaElement>("Value for MIXED");
    expect(value).toHaveValue("A\nB\n\nC\nD");

    dispatchTextareaEdit(value, {
      data: ">",
      inputType: "insertText",
      nextDisplayValue: ">A\nB\n\nC\nD",
      selectionStart: 0,
      selectionEnd: 0,
      nextSelection: 1,
    });
    dispatchTextareaEdit(value, {
      data: "!",
      inputType: "insertText",
      nextDisplayValue: ">A\nB!\n\nC\nD",
      selectionStart: 4,
      selectionEnd: 4,
      nextSelection: 5,
    });
    dispatchTextareaEdit(value, {
      data: "<",
      inputType: "insertText",
      nextDisplayValue: ">A\nB!\n\nC\nD<",
      selectionStart: 10,
      selectionEnd: 10,
      nextSelection: 11,
    });
    dispatchTextareaEdit(value, {
      data: null,
      inputType: "deleteContentForward",
      nextDisplayValue: ">A\nB!\nC\nD<",
      selectionStart: 5,
      selectionEnd: 5,
      nextSelection: 5,
    });
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1));
    expect(api.put).toHaveBeenCalledWith(
      "/projects/shop/environments/prod/config",
      {
        base_revision: 7,
        message: "",
        entries: [{ key: "MIXED", value: ">A\rB!\r\nC\nD<", service: "api" }],
      },
    );
  });

  it("rejects and announces an untracked edit that could corrupt raw line endings", () => {
    renderEditor(client(), {
      ...revision,
      entries: [{ key: "MIXED", value: "A\rB", service: "api" }],
    });
    const value = screen.getByLabelText<HTMLTextAreaElement>("Value for MIXED");

    fireEvent.change(value, { target: { value: "A\n!B" } });

    expect(value).toHaveValue("A\nB");
    expect(value).toHaveAccessibleDescription(
      "That edit wasn’t applied because the browser did not provide a reliable text range. The original line endings are unchanged.",
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "That edit wasn’t applied because the browser did not provide a reliable text range.",
    );
  });

  it("confirms draft-only deletion and restores or advances focus without an API request", async () => {
    const api = client();
    renderEditor(api, {
      ...revision,
      entries: [
        { key: "FIRST", value: "one", service: "api" },
        { key: "SECOND", value: "two", service: "worker" },
        { key: "THIRD", value: "three", service: "worker" },
      ],
    });
    const user = userEvent.setup();
    const deleteSecond = screen.getByRole("button", { name: "Delete SECOND" });

    await user.click(deleteSecond);
    const firstDialog = screen.getByRole("dialog", { name: "Remove SECOND from this draft?" });
    expect(screen.getByLabelText("Value for SECOND")).toHaveValue("two");
    expect(within(firstDialog).getByText(/removed from this draft only/iu)).toBeInTheDocument();
    expect(within(firstDialog).getByText(/not published until you save changes/iu)).toBeInTheDocument();
    await user.click(within(firstDialog).getByRole("button", { name: "Cancel" }));
    expect(deleteSecond).toHaveFocus();
    expect(screen.getByLabelText("Value for SECOND")).toBeInTheDocument();

    await user.click(deleteSecond);
    await user.keyboard("{Escape}");
    expect(deleteSecond).toHaveFocus();
    expect(screen.getByLabelText("Value for SECOND")).toBeInTheDocument();

    await user.click(deleteSecond);
    await user.click(screen.getByRole("button", { name: "Remove SECOND" }));
    expect(screen.queryByLabelText("Value for SECOND")).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByLabelText("Key for THIRD")).toHaveFocus());
    expect(api.put).not.toHaveBeenCalled();
  });

  it("moves focus to Add entry after confirming deletion of the final row", async () => {
    renderEditor();
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Delete EMPTY" }));
    await user.click(screen.getByRole("button", { name: "Remove EMPTY" }));

    await waitFor(() => expect(screen.getByRole("button", { name: "Add entry" })).toHaveFocus());
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

  it("relocalizes visible client validation without changing the invalid draft", async () => {
    renderEditor();
    const user = userEvent.setup();
    const key = screen.getByLabelText("Key for EMPTY");
    await user.clear(key);
    await user.type(key, "invalid key");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(key).toHaveAccessibleDescription(/Use letters, numbers/u);

    await act(async () => {
      await changeLocale("zh-CN");
    });

    const localizedKey = screen.getByLabelText("invalid key 的键");
    expect(localizedKey).toBe(key);
    expect(localizedKey).toHaveValue("invalid key");
    expect(localizedKey).toHaveAccessibleDescription("请使用字母、数字和下划线，并以字母或下划线开头。");
    expect(screen.getByRole("alert")).toHaveTextContent("请检查标记的字段后重试。");
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
    expect(screen.getByLabelText("Value for SECOND")).toHaveAccessibleDescription("The submitted value is invalid.");
    expect(screen.getByLabelText("Service for SECOND")).toHaveAccessibleDescription("The submitted service is invalid.");
    expect(screen.getByLabelText("Change message")).toHaveAccessibleDescription("Review the change message.");
    expect(screen.getByRole("alert")).toHaveTextContent("Review the marked fields");
    expect(screen.queryByText("SECRET")).not.toBeInTheDocument();
    expect(screen.queryByText("Value is too long.")).not.toBeInTheDocument();
    expect(screen.queryByText("Service is too long.")).not.toBeInTheDocument();
    expect(screen.queryByText("Message is required.")).not.toBeInTheDocument();
  });

  it("relocalizes safe server validation without recovering API-provided values", async () => {
    const api = client();
    vi.mocked(api.put).mockRejectedValue(
      new APIError(422, "validation_failed", "RAW ENVELOPE", "req", {
        "entries[0].value": "RAW FIELD VALUE",
        message: "RAW MESSAGE VALUE",
      }),
    );
    renderEditor(api);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Value for EMPTY"), "draft");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByLabelText("Value for EMPTY")).toHaveAccessibleDescription("The submitted value is invalid.");

    await act(async () => {
      await changeLocale("zh-CN");
    });

    expect(screen.getByLabelText("EMPTY 的值")).toHaveAccessibleDescription("提交的值无效。");
    expect(screen.getByLabelText("变更说明")).toHaveAccessibleDescription("请检查变更说明。");
    expect(document.body.textContent).not.toContain("RAW");
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
    expect(within(entriesEditor).getByText("Review the configuration entries.")).toBeInTheDocument();
    expect(entriesEditor).toHaveAccessibleDescription("Review the configuration entries.");
    expect(screen.getByLabelText("Value for EMPTY")).toHaveValue(" exact retained value ");
    expect(screen.getByLabelText("Change message")).toHaveValue("retained message");
    expect(screen.getByLabelText("Change message")).toHaveAccessibleDescription("Review the change message.");
    expect(screen.queryByText("RAW ENVELOPE SECRET")).not.toBeInTheDocument();
    expect(screen.queryByText("UNKNOWN FIELD SECRET")).not.toBeInTheDocument();
    expect(screen.queryByText("Combined entry content must be at most 1 byte.")).not.toBeInTheDocument();
    expect(screen.queryByText("Change message must be shorter.")).not.toBeInTheDocument();
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
    expect(screen.getByRole("textbox", { name: "Latest server value for SERVER_ONLY" }).textContent).toBe("");
    expect(screen.getByText("Absent", { selector: "span" })).toBeInTheDocument();

    await act(async () => {
      await changeLocale("zh-CN");
    });
    expect(screen.getByRole("heading", { name: "最新服务器版本与你的草稿对比" })).toBeInTheDocument();
    expect(screen.getByLabelText("EMPTY 的值")).toHaveValue("local draft  ");
    expect(screen.getByRole("button", { name: "将版本 8 用作新基准" })).toBeEnabled();
    await act(async () => {
      await changeLocale("en-US");
    });

    await user.click(screen.getByRole("button", { name: "Use version 8 as new base" }));
    expect(screen.getByRole("button", { name: "Save changes" })).toBeEnabled();
    const dirtyEvent = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(dirtyEvent);
    expect(dirtyEvent.defaultPrevented).toBe(true);
    await user.click(screen.getByRole("link", { name: "Project register" }));
    expect(screen.getByRole("dialog", { name: "Leave without saving?" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Stay" }));
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2));
    expect(vi.mocked(api.put).mock.calls[1]?.[1]).toMatchObject({ base_revision: 8 });
  });

  it("becomes clean when a reordered adopted server snapshot equals the normalized draft", async () => {
    const exactValue = " exact line  \n完整 😀 ";
    const api = client();
    vi.mocked(api.put).mockRejectedValue(
      new APIError(409, "revision_conflict", "SECRET", "req", {}),
    );
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        ...revision,
        id: "revision-8",
        version: 8,
        entries: [
          { key: "MIDDLE", value: "unchanged", service: "api" },
          { key: "Z_RENAMED", value: exactValue, service: "worker" },
        ],
      },
    });
    renderEditor(api, {
      ...revision,
      entries: [
        { key: "ORIGINAL", value: "old", service: "api" },
        { key: "MIDDLE", value: "unchanged", service: "api" },
      ],
    });
    const user = userEvent.setup();
    await user.clear(screen.getByLabelText("Key for ORIGINAL"));
    await user.type(screen.getByLabelText("Key for new entry"), " Z_RENAMED ");
    await user.clear(screen.getByLabelText("Value for Z_RENAMED"));
    await user.type(screen.getByLabelText("Value for Z_RENAMED"), exactValue);
    await user.clear(screen.getByLabelText("Service for Z_RENAMED"));
    await user.type(screen.getByLabelText("Service for Z_RENAMED"), " worker ");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await user.click(await screen.findByRole("button", { name: "Refresh and compare" }));

    expect(await screen.findByText("The snapshots contain the same entries.")).toBeInTheDocument();
    expect(screen.getByLabelText("Value for Z_RENAMED")).toHaveValue(exactValue);
    await user.click(screen.getByRole("button", { name: "Use version 8 as new base" }));

    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
    const cleanEvent = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(cleanEvent);
    expect(cleanEvent.defaultPrevented).toBe(false);
    await user.click(screen.getByRole("link", { name: "Project register" }));
    expect(await screen.findByRole("heading", { name: "Projects destination" })).toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: "Leave without saving?" })).not.toBeInTheDocument();
  });

  it("remains dirty after adopting a matching server snapshot with duplicate keys", async () => {
    const api = client();
    vi.mocked(api.put).mockRejectedValue(
      new APIError(409, "revision_conflict", "SECRET", "req", {}),
    );
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        ...revision,
        id: "revision-8",
        version: 8,
        entries: [
          { key: "FIRST", value: "one", service: "api" },
          { key: "FIRST", value: "two revised", service: "worker" },
        ],
      },
    });
    renderEditor(api, {
      ...revision,
      entries: [
        { key: "FIRST", value: "one", service: "api" },
        { key: "SECOND", value: "two", service: "worker" },
      ],
    });
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Value for SECOND"), " revised");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Configuration changed since you loaded it",
    );

    const secondKey = screen.getByLabelText("Key for SECOND");
    await user.clear(secondKey);
    await user.type(secondKey, " FIRST ");
    await user.click(screen.getByRole("button", { name: "Refresh and compare" }));
    await user.click(await screen.findByRole("button", { name: "Use version 8 as new base" }));

    expect(screen.getByRole("button", { name: "Save changes" })).toBeEnabled();
    const dirtyEvent = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(dirtyEvent);
    expect(dirtyEvent.defaultPrevented).toBe(true);
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
