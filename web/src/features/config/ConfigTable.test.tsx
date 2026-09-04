import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StrictMode, useState } from "react";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { APIError } from "../../api/client";
import type { APIClientContract } from "../../api/types";
import { changeLocale } from "../../i18n";
import { ConfigTable } from "./ConfigTable";

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

describe("ConfigTable", () => {
  it("directs the user to choose an environment without loading configuration", () => {
    const api = client();
    render(
      <ConfigTable
        client={api}
        projectSlug="shop"
        environmentSlug=""
        canWrite
        refreshEpoch={0}
        onRevisionChanged={vi.fn()}
      />,
    );

    expect(screen.getByRole("heading", { name: "Choose an environment" })).toBeInTheDocument();
    expect(api.get).not.toHaveBeenCalled();
  });

  it("loads encoded current configuration and renders exact plain values", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-8",
        environment_id: "env-prod",
        message: "exact values",
        created_by: "user-admin",
        created_by_type: "user",
        version: 8,
        created_at: "2026-08-29T08:00:00Z",
        entries: [
          { key: "EMPTY", value: "", service: "api" },
          { key: "MULTILINE", value: "第一行 😀\nsecond line  ", service: "worker" },
        ],
      },
    });

    render(
      <StrictMode>
        <ConfigTable
          client={api}
          projectSlug="shop/intl"
          environmentSlug="prod west"
          canWrite
          refreshEpoch={0}
          onRevisionChanged={vi.fn()}
        />
      </StrictMode>,
    );

    expect(screen.getByRole("status")).toHaveTextContent("Loading configuration");
    const table = await screen.findByRole("table", { name: "Current configuration" });
    expect(within(table).getAllByRole("columnheader").map((cell) => cell.textContent)).toEqual([
      "Key",
      "Value",
      "Service",
      "Actions",
    ]);
    const multiline = within(table).getByTestId("configuration-value-MULTILINE");
    expect(multiline.textContent).toBe("第一行 😀\nsecond line  ");
    expect(within(table).getByTestId("configuration-value-EMPTY")).toHaveTextContent("Empty string");
    await waitFor(() =>
      expect(api.get).toHaveBeenCalledWith(
        "/projects/shop%2Fintl/environments/prod%20west/config",
      ),
    );
  });

  it("keeps add and edit dialogs available in a narrow viewport", async () => {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query === "(max-width: 759px)",
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-8",
        environment_id: "env-prod",
        message: "mobile read",
        created_by: "user-admin",
        created_by_type: "user",
        version: 8,
        created_at: "2026-08-29T08:00:00Z",
        entries: [
          { key: "DATABASE_URL", value: "postgres://exact", service: "api" },
        ],
      },
    });
    const router = createMemoryRouter([{
      path: "/",
      element: (
        <ConfigTable
          client={api}
          projectSlug="shop"
          environmentSlug="prod"
          canWrite
          refreshEpoch={0}
          onRevisionChanged={vi.fn()}
        />
      ),
    }]);
    render(<RouterProvider router={router} />);

    const user = userEvent.setup();
    expect(await screen.findByTestId("configuration-value-DATABASE_URL")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add configuration" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Edit DATABASE_URL" }));
    expect(screen.getByRole("dialog", { name: "Edit configuration entry" })).toBeInTheDocument();
    expect(screen.queryByText(/desktop viewport is required to edit/iu)).not.toBeInTheDocument();
    vi.unstubAllGlobals();
  });

  it("retains the exact dirty draft and leave protection across a live desktop boundary", async () => {
    let matches = false;
    const listeners = new Set<() => void>();
    vi.stubGlobal("matchMedia", (query: string) => ({
      get matches() {
        return matches;
      },
      media: query,
      onchange: null,
      addEventListener: (_type: string, listener: () => void) => listeners.add(listener),
      removeEventListener: (_type: string, listener: () => void) => listeners.delete(listener),
      addListener: (listener: () => void) => listeners.add(listener),
      removeListener: (listener: () => void) => listeners.delete(listener),
      dispatchEvent: vi.fn(),
    }));
    const setMobile = (next: boolean) => {
      matches = next;
      act(() => listeners.forEach((listener) => listener()));
    };
    const api = client();
    const loaded = {
      id: "revision-7",
      environment_id: "env-prod",
      message: "current",
      created_by: "user-admin",
      created_by_type: "user",
      version: 7,
      created_at: "2026-08-29T08:00:00Z",
      entries: [{ key: "MIXED", value: "A\r\nB\rC\n", service: "api" }],
    };
    vi.mocked(api.get).mockResolvedValue({ revision: loaded });
    vi.mocked(api.put).mockResolvedValue({
      revision: { ...loaded, id: "revision-8", version: 8 },
    });
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <>
            <a href="/elsewhere" onClick={(event) => {
              event.preventDefault();
              void router.navigate("/elsewhere");
            }}>Elsewhere</a>
            <ConfigTable
              client={api}
              projectSlug="shop"
              environmentSlug="prod"
              canWrite
              refreshEpoch={0}
              onRevisionChanged={vi.fn()}
            />
          </>
        ),
      },
      { path: "/elsewhere", element: <h1>Elsewhere</h1> },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit MIXED" }));
    await user.type(screen.getByLabelText("Service"), " worker");
    await user.type(screen.getByLabelText("Change message"), "retain exact draft");

    setMobile(true);
    expect(screen.getByRole("dialog", { name: "Edit configuration entry" })).toBeInTheDocument();
    expect(screen.queryByText(/desktop viewport is required to edit/iu)).not.toBeInTheDocument();
    expect(window.dispatchEvent(new Event("beforeunload", { cancelable: true }))).toBe(false);
    await user.click(screen.getByRole("link", { name: "Elsewhere" }));
    expect(screen.getByRole("dialog", { name: "Discard unsaved entry changes?" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Keep editing" }));

    setMobile(false);
    expect(screen.getByLabelText("Value")).toHaveValue("A\nB\nC\n");
    expect(screen.getByLabelText("Service")).toHaveValue("api worker");
    expect(screen.getByLabelText("Change message")).toHaveValue("retain exact draft");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(api.put).toHaveBeenCalledWith(
      "/projects/shop/environments/prod/config",
      {
        base_revision: 7,
        message: "retain exact draft",
        entries: [{ key: "MIXED", value: "A\r\nB\rC\n", service: "api worker" }],
      },
    ));
    vi.unstubAllGlobals();
  });

  it("filters current entries by key or service without changing values", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-2",
        environment_id: "env-prod",
        message: "two",
        created_by: "user-admin",
        created_by_type: "user",
        version: 2,
        created_at: "2026-08-29T08:00:00Z",
        entries: [
          { key: "DATABASE_URL", value: " postgres://exact ", service: "api" },
          { key: "QUEUE", value: "jobs", service: "worker" },
        ],
      },
    });
    render(
      <ConfigTable
        client={api}
        projectSlug="shop"
        environmentSlug="prod"
        canWrite
        refreshEpoch={0}
        onRevisionChanged={vi.fn()}
      />,
    );
    const user = userEvent.setup();
    await screen.findByText("DATABASE_URL");

    await user.type(screen.getByRole("searchbox", { name: "Search configuration" }), "worker");
    expect(screen.queryByText("DATABASE_URL")).not.toBeInTheDocument();
    expect(screen.getByText("QUEUE")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Clear configuration search" }));
    expect(screen.getByRole("searchbox", { name: "Search configuration" })).toHaveFocus();
    expect(screen.getByText("DATABASE_URL")).toBeInTheDocument();
    await user.type(screen.getByRole("searchbox", { name: "Search configuration" }), "database");
    expect(screen.getByTestId("configuration-value-DATABASE_URL").textContent).toBe(" postgres://exact ");
  });

  it("localizes configuration copy and clear search without translating stored data", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-2",
        environment_id: "env-prod",
        message: "business message",
        created_by: "user-admin",
        created_by_type: "user",
        version: 2,
        created_at: "2026-08-29T08:00:00Z",
        entries: [{ key: "DATABASE_URL", value: " 原始业务值 ", service: "api-service" }],
      },
    });
    await act(async () => {
      await changeLocale("zh-CN");
    });
    render(
      <ConfigTable
        client={api}
        projectSlug="shop"
        environmentSlug="prod"
        canWrite
        refreshEpoch={0}
        onRevisionChanged={vi.fn()}
      />,
    );
    const user = userEvent.setup();

    const table = await screen.findByRole("table", { name: "当前配置" });
    expect(within(table).getAllByRole("columnheader").map((cell) => cell.textContent)).toEqual([
      "键",
      "值",
      "服务",
      "操作",
    ]);
    expect(within(table).getByTestId("configuration-value-DATABASE_URL").textContent).toBe(" 原始业务值 ");
    expect(within(table).getByText("api-service")).toBeInTheDocument();
    const search = screen.getByRole("searchbox", { name: "搜索配置" });
    await user.type(search, "no-match");
    expect(screen.getByText("没有与此搜索匹配的键或服务。")).toBeInTheDocument();
    const clear = screen.getByRole("button", { name: "清除配置搜索" });
    await user.click(clear);
    expect(search).toHaveFocus();
    expect(screen.getByText("DATABASE_URL")).toBeInTheDocument();
  });

  it("shows a safe retry state and loads the empty snapshot on retry", async () => {
    const api = client();
    vi.mocked(api.get)
      .mockRejectedValueOnce(new Error("SECRET remote detail"))
      .mockResolvedValueOnce({
        revision: {
          id: "",
          environment_id: "env-stage",
          message: "",
          created_by: "",
          created_by_type: "user",
          version: 0,
          created_at: "",
          entries: [],
        },
      });
    render(
      <ConfigTable
        client={api}
        projectSlug="shop"
        environmentSlug="stage"
        canWrite
        refreshEpoch={0}
        onRevisionChanged={vi.fn()}
      />,
    );
    const user = userEvent.setup();

    const retry = await screen.findByRole("button", { name: "Retry" });
    retry.focus();
    expect(retry).toHaveFocus();
    expect(screen.queryByText(/SECRET/u)).not.toBeInTheDocument();
    await user.click(retry);
    expect(await screen.findByRole("heading", { name: "No configuration entries" })).toBeInTheDocument();
    expect(api.get).toHaveBeenCalledTimes(2);
  });

  it("keeps viewer configuration read-only", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-1",
        environment_id: "env-prod",
        message: "one",
        created_by: "user-admin",
        created_by_type: "user",
        version: 1,
        created_at: "2026-08-29T08:00:00Z",
        entries: [{ key: "VISIBLE", value: "plain", service: "" }],
      },
    });
    render(
      <ConfigTable
        client={api}
        projectSlug="shop"
        environmentSlug="prod"
        canWrite={false}
        refreshEpoch={0}
        onRevisionChanged={vi.fn()}
      />,
    );

    expect(await screen.findByText("VISIBLE")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add configuration" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit VISIBLE" })).not.toBeInTheDocument();
    expect(screen.getAllByRole("columnheader")).toHaveLength(3);
  });

  it("hands focus across edit, dirty cancel, and save while announcing the saved revision in StrictMode", async () => {
    const api = client();
    const loaded = {
      id: "revision-7",
      environment_id: "env-prod",
      message: "current",
      created_by: "user-admin",
      created_by_type: "user",
      version: 7,
      created_at: "2026-08-29T08:00:00Z",
      entries: [{ key: "EMPTY", value: "", service: "api" }],
    };
    vi.mocked(api.get).mockResolvedValue({ revision: loaded });
    vi.mocked(api.put).mockResolvedValue({
      revision: {
        ...loaded,
        id: "revision-8",
        version: 8,
        entries: [{ key: "EMPTY", value: "saved exact", service: "api" }],
      },
    });
    const router = createMemoryRouter([{
      path: "/",
      element: (
        <StrictMode>
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        </StrictMode>
      ),
    }]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();
    const edit = await screen.findByRole("button", { name: "Edit EMPTY" });

    await user.click(edit);
    await waitFor(() => expect(screen.getByLabelText("Key")).toHaveFocus());
    await user.type(screen.getByLabelText("Value"), "discard me");
    const editDialog = screen.getByRole("dialog", { name: "Edit configuration entry" });
    await user.click(within(editDialog).getAllByRole("button", { name: "Cancel" })[0]);
    await user.click(screen.getByRole("button", { name: "Discard changes" }));
    const restoredEdit = await screen.findByRole("button", { name: "Edit EMPTY" });
    await waitFor(() => expect(restoredEdit).toHaveFocus());

    await user.click(restoredEdit);
    await waitFor(() => expect(screen.getByLabelText("Key")).toHaveFocus());
    await user.type(screen.getByLabelText("Value"), "saved exact");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    const configurationHeading = await screen.findByRole("heading", { name: "Configuration" });
    await waitFor(() => expect(configurationHeading).toHaveFocus());
    expect(screen.getByRole("status")).toHaveTextContent("Revision 8 saved");
  });

  it("clears the saved announcement when another environment loads", async () => {
    const api = client();
    const revision = (environment: string, version: number, key: string) => ({
      id: `revision-${environment}-${version}`,
      environment_id: `env-${environment}`,
      message: environment,
      created_by: "user-admin",
      created_by_type: "user",
      version,
      created_at: "2026-08-29T08:00:00Z",
      entries: [{ key, value: environment, service: "api" }],
    });
    vi.mocked(api.get).mockImplementation((path) => Promise.resolve({
      revision: path.includes("/stage/")
        ? revision("stage", 3, "STAGE_ONLY")
        : revision("prod", 7, "PROD_ONLY"),
    }));
    vi.mocked(api.put).mockResolvedValue({ revision: revision("prod", 8, "PROD_ONLY") });
    function Harness() {
      const [environment, setEnvironment] = useState("prod");
      const [epoch, setEpoch] = useState(0);
      return (
        <>
          <button type="button" onClick={() => setEnvironment("stage")}>Switch to stage</button>
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug={environment}
            canWrite
            refreshEpoch={epoch}
            onRevisionChanged={() => setEpoch((current) => current + 1)}
          />
        </>
      );
    }
    const router = createMemoryRouter([{ path: "/", element: <Harness /> }]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit PROD_ONLY" }));
    await waitFor(() => expect(screen.getByLabelText("Key")).toHaveFocus());
    const value = screen.getByLabelText("Value");
    await user.type(value, " saved");
    expect(value).toHaveValue("prod saved");
    const save = screen.getByRole("button", { name: "Save changes" });
    await waitFor(() => expect(save).toBeEnabled());
    await user.click(save);
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1));
    expect(await screen.findByRole("status")).toHaveTextContent("Revision 8 saved");

    await user.click(screen.getByRole("button", { name: "Switch to stage" }));
    expect(await screen.findByText("STAGE_ONLY")).toBeInTheDocument();
    expect(screen.queryByText("Revision 8 saved.")).not.toBeInTheDocument();
  });

  it("discards a stale environment response", async () => {
    let resolveProd!: (value: unknown) => void;
    const prod = new Promise((resolve) => {
      resolveProd = resolve;
    });
    const api = client();
    vi.mocked(api.get).mockImplementation((path) => {
      if (path.includes("/prod/")) {
        return prod as Promise<never>;
      }
      return Promise.resolve({
        revision: {
          id: "revision-stage",
          environment_id: "env-stage",
          message: "stage",
          created_by: "user-admin",
          created_by_type: "user",
          version: 3,
          created_at: "2026-08-29T08:00:00Z",
          entries: [{ key: "STAGE_ONLY", value: "stage", service: "" }],
        },
      });
    });
    const props = {
      client: api,
      projectSlug: "shop",
      canWrite: true,
      refreshEpoch: 0,
      onRevisionChanged: vi.fn(),
    };
    const view = render(<ConfigTable {...props} environmentSlug="prod" />);
    view.rerender(<ConfigTable {...props} environmentSlug="stage" />);

    expect(await screen.findByText("STAGE_ONLY")).toBeInTheDocument();
    resolveProd({
      revision: {
        id: "revision-prod",
        environment_id: "env-prod",
        message: "prod",
        created_by: "user-admin",
        created_by_type: "user",
        version: 9,
        created_at: "2026-08-29T08:00:00Z",
        entries: [{ key: "STALE_PROD", value: "prod", service: "" }],
      },
    });
    await waitFor(() => expect(screen.queryByText("STALE_PROD")).not.toBeInTheDocument());
    expect(screen.getByText("STAGE_ONLY")).toBeInTheDocument();
  });

  it("opens a prefilled entry dialog from the row edit action", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-4",
        environment_id: "env-prod",
        message: "current",
        created_by: "user-admin",
        created_by_type: "user",
        version: 4,
        created_at: "2026-08-29T08:00:00Z",
        entries: [
          { key: "DATABASE_URL", value: "postgres://exact", service: "api" },
        ],
      },
    });
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit DATABASE_URL" }));

    const dialog = screen.getByRole("dialog", { name: "Edit configuration entry" });
    expect(within(dialog).getByLabelText("Key")).toHaveValue("DATABASE_URL");
    expect(within(dialog).getByLabelText("Value")).toHaveValue("postgres://exact");
    expect(within(dialog).getByLabelText("Service")).toHaveValue("api");
  });

  it("opens a blank entry dialog from the add action", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-4",
        environment_id: "env-prod",
        message: "current",
        created_by: "user-admin",
        created_by_type: "user",
        version: 4,
        created_at: "2026-08-29T08:00:00Z",
        entries: [],
      },
    });
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Add configuration" }));

    const dialog = screen.getByRole("dialog", { name: "Add configuration entry" });
    expect(within(dialog).getByLabelText("Key")).toHaveValue("");
    expect(within(dialog).getByLabelText("Value")).toHaveValue("");
    expect(within(dialog).getByLabelText("Service")).toHaveValue("");
  });

  it("saves one edited row while preserving the complete configuration snapshot", async () => {
    const api = client();
    const loaded = {
      id: "revision-4",
      environment_id: "env-prod",
      message: "current",
      created_by: "user-admin",
      created_by_type: "user" as const,
      version: 4,
      created_at: "2026-08-29T08:00:00Z",
      entries: [
        { key: "DATABASE_URL", value: "postgres://before", service: "api" },
        { key: "QUEUE", value: "jobs", service: "worker" },
      ],
    };
    vi.mocked(api.get).mockResolvedValue({ revision: loaded });
    vi.mocked(api.put).mockResolvedValue({
      revision: {
        ...loaded,
        id: "revision-5",
        version: 5,
        entries: [
          { key: "DATABASE_URL", value: "postgres://after", service: "backend" },
          { key: "QUEUE", value: "jobs", service: "worker" },
        ],
      },
    });
    const onRevisionChanged = vi.fn();
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={onRevisionChanged}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit DATABASE_URL" }));
    const dialog = screen.getByRole("dialog", { name: "Edit configuration entry" });
    await user.clear(within(dialog).getByLabelText("Value"));
    await user.type(within(dialog).getByLabelText("Value"), "postgres://after");
    await user.clear(within(dialog).getByLabelText("Service"));
    await user.type(within(dialog).getByLabelText("Service"), "backend");
    await user.type(within(dialog).getByLabelText("Change message"), "rotate database");
    await user.click(within(dialog).getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(api.put).toHaveBeenCalledWith(
      "/projects/shop/environments/prod/config",
      {
        base_revision: 4,
        message: "rotate database",
        entries: [
          { key: "DATABASE_URL", value: "postgres://after", service: "backend" },
          { key: "QUEUE", value: "jobs", service: "worker" },
        ],
      },
    ));
    expect(await screen.findByText("Revision 5 saved.")).toBeInTheDocument();
    expect(screen.getByTestId("configuration-value-DATABASE_URL")).toHaveTextContent("postgres://after");
    expect(screen.getByTestId("configuration-value-QUEUE")).toHaveTextContent("jobs");
    expect(onRevisionChanged).toHaveBeenCalledWith(expect.objectContaining({ version: 5 }));
  });

  it("rejects a duplicate trimmed key before creating an entry", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-4",
        environment_id: "env-prod",
        message: "current",
        created_by: "user-admin",
        created_by_type: "user",
        version: 4,
        created_at: "2026-08-29T08:00:00Z",
        entries: [{ key: "DATABASE_URL", value: "postgres://current", service: "api" }],
      },
    });
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Add configuration" }));
    const dialog = screen.getByRole("dialog", { name: "Add configuration entry" });
    await user.type(within(dialog).getByLabelText("Key"), " DATABASE_URL ");
    await user.click(within(dialog).getByRole("button", { name: "Save changes" }));

    expect(within(dialog).getByLabelText("Key")).toHaveAttribute("aria-invalid", "true");
    expect(within(dialog).getByText("Each key must be unique.")).toBeInTheDocument();
    expect(within(dialog).getByRole("alert")).toHaveTextContent("Review the marked fields and try again.");
    expect(api.put).not.toHaveBeenCalled();
  });

  it("reveals an overflowing compact value on hover and keyboard focus", async () => {
    const api = client();
    const longValue = "a-long-configuration-value-that-does-not-fit-in-one-row";
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-4",
        environment_id: "env-prod",
        message: "current",
        created_by: "user-admin",
        created_by_type: "user",
        version: 4,
        created_at: "2026-08-29T08:00:00Z",
        entries: [{ key: "LONG_VALUE", value: longValue, service: "api" }],
      },
    });
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const value = await screen.findByTestId("configuration-value-LONG_VALUE");
    Object.defineProperties(value, {
      clientWidth: { configurable: true, value: 120 },
      scrollWidth: { configurable: true, value: 420 },
    });
    fireEvent(window, new Event("resize"));
    vi.useFakeTimers();

    fireEvent.mouseEnter(value);
    await act(async () => vi.advanceTimersByTime(300));
    expect(screen.getByRole("tooltip")).toHaveTextContent(longValue);

    fireEvent.mouseLeave(value);
    await act(async () => vi.advanceTimersByTime(100));
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();

    fireEvent.focus(value);
    await act(async () => vi.advanceTimersByTime(300));
    expect(screen.getByRole("tooltip")).toHaveTextContent(longValue);
    fireEvent.keyDown(value, { key: "Escape" });
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
    vi.useRealTimers();
  });

  it("confirms and publishes deletion from the edit dialog", async () => {
    const api = client();
    const loaded = {
      id: "revision-4",
      environment_id: "env-prod",
      message: "current",
      created_by: "user-admin",
      created_by_type: "user" as const,
      version: 4,
      created_at: "2026-08-29T08:00:00Z",
      entries: [
        { key: "DATABASE_URL", value: "postgres://current", service: "api" },
        { key: "QUEUE", value: "jobs", service: "worker" },
      ],
    };
    vi.mocked(api.get).mockResolvedValue({ revision: loaded });
    vi.mocked(api.put).mockResolvedValue({
      revision: {
        ...loaded,
        id: "revision-5",
        version: 5,
        entries: [{ key: "QUEUE", value: "jobs", service: "worker" }],
      },
    });
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit DATABASE_URL" }));
    await user.type(screen.getByLabelText("Change message"), "remove database");
    await user.click(screen.getByRole("button", { name: "Delete configuration" }));
    expect(screen.getByRole("dialog", { name: "Delete DATABASE_URL?" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete DATABASE_URL" }));

    await waitFor(() => expect(api.put).toHaveBeenCalledWith(
      "/projects/shop/environments/prod/config",
      {
        base_revision: 4,
        message: "remove database",
        entries: [{ key: "QUEUE", value: "jobs", service: "worker" }],
      },
    ));
    expect(screen.queryByText("DATABASE_URL")).not.toBeInTheDocument();
    expect(screen.getByText("QUEUE")).toBeInTheDocument();
  });

  it("keeps the delete confirmation recoverable when deletion fails", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-4",
        environment_id: "env-prod",
        message: "current",
        created_by: "user-admin",
        created_by_type: "user",
        version: 4,
        created_at: "2026-08-29T08:00:00Z",
        entries: [{ key: "DATABASE_URL", value: "postgres://before", service: "api" }],
      },
    });
    vi.mocked(api.put).mockRejectedValue(new Error("private network detail"));
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit DATABASE_URL" }));
    await user.click(screen.getByRole("button", { name: "Delete configuration" }));
    await user.click(screen.getByRole("button", { name: "Delete DATABASE_URL" }));

    const dialog = await screen.findByRole("dialog", { name: "Delete DATABASE_URL?" });
    expect(within(dialog).getByRole("alert")).toHaveTextContent(
      "The configuration entry couldn’t be deleted. Nothing was changed; try again.",
    );
    expect(within(dialog).getByRole("button", { name: "Delete DATABASE_URL" })).toBeEnabled();
    expect(screen.queryByText("private network detail")).not.toBeInTheDocument();
  });

  it("retains the entry draft when saving fails", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-4",
        environment_id: "env-prod",
        message: "current",
        created_by: "user-admin",
        created_by_type: "user",
        version: 4,
        created_at: "2026-08-29T08:00:00Z",
        entries: [{ key: "DATABASE_URL", value: "postgres://before", service: "api" }],
      },
    });
    vi.mocked(api.put).mockRejectedValue(new Error("network detail"));
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit DATABASE_URL" }));
    await user.clear(screen.getByLabelText("Value"));
    await user.type(screen.getByLabelText("Value"), "postgres://unsaved");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    const dialog = await screen.findByRole("dialog", { name: "Edit configuration entry" });
    expect(within(dialog).getByLabelText("Value")).toHaveValue("postgres://unsaved");
    expect(within(dialog).getByRole("alert")).toHaveTextContent(
      "The configuration entry couldn’t be saved. Your changes are still here; try again.",
    );
  });

  it("maps server validation to the submitted entry fields without exposing server detail", async () => {
    const api = client();
    const loaded = {
      id: "revision-4",
      environment_id: "env-prod",
      message: "current",
      created_by: "user-admin",
      created_by_type: "user" as const,
      version: 4,
      created_at: "2026-08-29T08:00:00Z",
      entries: [{ key: "DATABASE_URL", value: "postgres://before", service: "api" }],
    };
    vi.mocked(api.get).mockResolvedValue({ revision: loaded });
    vi.mocked(api.put).mockRejectedValue(new APIError(422, "validation_error", "private detail", "req", {
      "entries[0].service": "secret server detail",
      message: "another private detail",
    }));
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit DATABASE_URL" }));
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    expect(await screen.findByText("The submitted service is invalid.")).toBeInTheDocument();
    expect(screen.getByLabelText("Service")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText("Review the change message.")).toBeInTheDocument();
    expect(screen.getByLabelText("Change message")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("alert")).toHaveTextContent("Review the marked fields and try again.");
    await waitFor(() => expect(screen.getByLabelText("Service")).toHaveFocus());
    expect(screen.queryByText(/private detail|secret server detail/iu)).not.toBeInTheDocument();
  });

  it("does not publish the same entry twice while its first save is pending", async () => {
    const api = client();
    const loaded = {
      id: "revision-4",
      environment_id: "env-prod",
      message: "current",
      created_by: "user-admin",
      created_by_type: "user" as const,
      version: 4,
      created_at: "2026-08-29T08:00:00Z",
      entries: [{ key: "DATABASE_URL", value: "postgres://before", service: "api" }],
    };
    let resolveSave!: (value: { revision: typeof loaded }) => void;
    vi.mocked(api.get).mockResolvedValue({ revision: loaded });
    vi.mocked(api.put).mockReturnValue(new Promise((resolve) => {
      resolveSave = resolve;
    }));
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit DATABASE_URL" }));
    const save = screen.getByRole("button", { name: "Save changes" });
    const form = save.closest("form");
    expect(form).not.toBeNull();
    fireEvent.submit(form as HTMLFormElement);
    fireEvent.submit(form as HTMLFormElement);

    expect(api.put).toHaveBeenCalledTimes(1);
    resolveSave({ revision: { ...loaded, version: 5 } });
    expect(await screen.findByText("Revision 5 saved.")).toBeInTheDocument();
  });

  it("keeps a visible draft and relocalizes validation when the locale changes", async () => {
    const api = client();
    const loaded = {
      id: "revision-4",
      environment_id: "env-prod",
      message: "current",
      created_by: "user-admin",
      created_by_type: "user" as const,
      version: 4,
      created_at: "2026-08-29T08:00:00Z",
      entries: [{ key: "DATABASE_URL", value: "postgres://before", service: "api" }],
    };
    vi.mocked(api.get).mockResolvedValue({ revision: loaded });
    vi.mocked(api.put).mockRejectedValue(new APIError(422, "validation_error", "private", "req", {
      "entries[0].service": "private",
    }));
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit DATABASE_URL" }));
    await user.clear(screen.getByLabelText("Service"));
    await user.type(screen.getByLabelText("Service"), "backend-draft");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("The submitted service is invalid.")).toBeInTheDocument();

    await act(async () => {
      await changeLocale("zh-CN");
    });

    expect(screen.getByLabelText("服务")).toHaveValue("backend-draft");
    expect(screen.getByText("提交的服务无效。")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("请检查标记的字段后重试。");
  });

  it("preserves untouched mixed line endings while editing one entry value", async () => {
    const api = client();
    const loaded = {
      id: "revision-7",
      environment_id: "env-prod",
      message: "current",
      created_by: "user-admin",
      created_by_type: "user" as const,
      version: 7,
      created_at: "2026-08-29T08:00:00Z",
      entries: [{ key: "MIXED", value: "A\rB\r\r\nC\nD", service: "api" }],
    };
    vi.mocked(api.get).mockResolvedValue({ revision: loaded });
    vi.mocked(api.put).mockResolvedValue({ revision: { ...loaded, version: 8 } });
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit MIXED" }));
    await user.click(screen.getByRole("button", { name: "Delete configuration" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    const value = screen.getByLabelText<HTMLTextAreaElement>("Value");
    expect(value).toHaveValue("A\nB\n\nC\nD");
    dispatchTextareaEdit(value, {
      data: "!",
      inputType: "insertText",
      nextDisplayValue: "A\nB!\n\nC\nD",
      selectionStart: 3,
      selectionEnd: 3,
      nextSelection: 4,
    });
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(api.put).toHaveBeenCalledWith(
      "/projects/shop/environments/prod/config",
      {
        base_revision: 7,
        message: "",
        entries: [{ key: "MIXED", value: "A\rB!\r\r\nC\nD", service: "api" }],
      },
    ));
  });

  it("rebases one entry onto the latest configuration after a conflict", async () => {
    const api = client();
    const loaded = {
      id: "revision-4",
      environment_id: "env-prod",
      message: "current",
      created_by: "user-admin",
      created_by_type: "user" as const,
      version: 4,
      created_at: "2026-08-29T08:00:00Z",
      entries: [{ key: "DATABASE_URL", value: "postgres://before", service: "api" }],
    };
    const latest = {
      ...loaded,
      id: "revision-5",
      version: 5,
      entries: [
        { key: "DATABASE_URL", value: "postgres://server", service: "server-service" },
        { key: "SERVER_ADDED", value: "keep-me", service: "worker" },
      ],
    };
    const saved = {
      ...latest,
      id: "revision-6",
      version: 6,
      entries: [
        { key: "DATABASE_URL", value: "postgres://mine", service: "api" },
        { key: "SERVER_ADDED", value: "keep-me", service: "worker" },
      ],
    };
    vi.mocked(api.get)
      .mockResolvedValueOnce({ revision: loaded })
      .mockResolvedValueOnce({ revision: latest });
    vi.mocked(api.put)
      .mockRejectedValueOnce(new APIError(409, "revision_conflict", "private", "req", {}))
      .mockResolvedValueOnce({ revision: saved });
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit DATABASE_URL" }));
    await user.clear(screen.getByLabelText("Value"));
    await user.type(screen.getByLabelText("Value"), "postgres://mine");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Configuration changed since you opened this entry.",
    );

    await user.click(screen.getByRole("button", { name: "Refresh and compare" }));
    expect(await screen.findByText("postgres://server")).toBeInTheDocument();
    const comparison = screen.getByRole("heading", {
      name: "Latest server entry compared with your draft",
    }).closest("section");
    expect(comparison).not.toBeNull();
    expect(within(comparison as HTMLElement).getByText("postgres://mine")).toBeInTheDocument();
    expect(within(comparison as HTMLElement).getByText("server-service")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Use version 5 as new base" }));
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(api.put).toHaveBeenLastCalledWith(
      "/projects/shop/environments/prod/config",
      {
        base_revision: 5,
        message: "",
        entries: [
          { key: "DATABASE_URL", value: "postgres://mine", service: "api" },
          { key: "SERVER_ADDED", value: "keep-me", service: "worker" },
        ],
      },
    ));
    expect(await screen.findByText("Revision 6 saved.")).toBeInTheDocument();
  });

  it("protects a dirty entry dialog before discarding its draft", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-4",
        environment_id: "env-prod",
        message: "current",
        created_by: "user-admin",
        created_by_type: "user",
        version: 4,
        created_at: "2026-08-29T08:00:00Z",
        entries: [{ key: "DATABASE_URL", value: "postgres://before", service: "api" }],
      },
    });
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <ConfigTable
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={0}
            onRevisionChanged={vi.fn()}
          />
        ),
      },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit DATABASE_URL" }));
    await user.clear(screen.getByLabelText("Value"));
    await user.type(screen.getByLabelText("Value"), "postgres://draft");
    await user.click(screen.getAllByRole("button", { name: "Cancel" })[0]);

    expect(screen.getByRole("dialog", { name: "Discard unsaved entry changes?" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Keep editing" }));
    expect(screen.getByLabelText("Value")).toHaveValue("postgres://draft");
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "Discard changes" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit DATABASE_URL" })).toHaveFocus();
  });

  it("blocks route navigation while an entry dialog has unsaved changes", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-4",
        environment_id: "env-prod",
        message: "current",
        created_by: "user-admin",
        created_by_type: "user",
        version: 4,
        created_at: "2026-08-29T08:00:00Z",
        entries: [{ key: "DATABASE_URL", value: "postgres://before", service: "api" }],
      },
    });
    const router = createMemoryRouter([
      {
        path: "/",
        element: (
          <>
            <a href="/elsewhere" onClick={(event) => {
              event.preventDefault();
              void router.navigate("/elsewhere");
            }}>Elsewhere</a>
            <ConfigTable
              client={api}
              projectSlug="shop"
              environmentSlug="prod"
              canWrite
              refreshEpoch={0}
              onRevisionChanged={vi.fn()}
            />
          </>
        ),
      },
      { path: "/elsewhere", element: <h1>Elsewhere destination</h1> },
    ]);
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Edit DATABASE_URL" }));
    await user.type(screen.getByLabelText("Service"), "-draft");
    await user.click(screen.getByRole("link", { name: "Elsewhere" }));

    expect(screen.getByRole("dialog", { name: "Discard unsaved entry changes?" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Elsewhere destination" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Discard changes" }));
    expect(await screen.findByRole("heading", { name: "Elsewhere destination" })).toBeInTheDocument();
  });
});
