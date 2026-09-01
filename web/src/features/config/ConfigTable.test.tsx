import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StrictMode, useState } from "react";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
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
    ]);
    const multiline = within(table).getByRole("textbox", { name: "Stored value for MULTILINE" });
    expect(multiline.textContent).toBe("第一行 😀\nsecond line  ");
    expect(multiline).toHaveAttribute("aria-readonly", "true");
    expect(within(table).getByTestId("configuration-value-MULTILINE")).toBe(multiline);
    expect(within(table).getByRole("textbox", { name: "Stored value for EMPTY" }).textContent).toBe("");
    await waitFor(() =>
      expect(api.get).toHaveBeenCalledWith(
        "/projects/shop%2Fintl/environments/prod%20west/config",
      ),
    );
  });

  it("keeps configuration readable but requires a desktop viewport to edit", async () => {
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

    expect(await screen.findByTestId("configuration-value-DATABASE_URL")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit configuration" })).not.toBeInTheDocument();
    expect(screen.getByText(/desktop viewport is required to edit/iu)).toBeInTheDocument();
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

    await user.click(await screen.findByRole("button", { name: "Edit configuration" }));
    await user.type(screen.getByLabelText("Service for MIXED"), " worker");
    await user.type(screen.getByLabelText("Change message"), "retain exact draft");

    setMobile(true);
    expect(screen.queryByRole("heading", { name: "Edit configuration" })).not.toBeInTheDocument();
    expect(screen.getByText(/desktop viewport is required to edit/iu)).toBeInTheDocument();
    expect(window.dispatchEvent(new Event("beforeunload", { cancelable: true }))).toBe(false);
    await user.click(screen.getByRole("link", { name: "Elsewhere" }));
    expect(screen.getByRole("dialog", { name: "Leave without saving?" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Stay" }));

    setMobile(false);
    expect(screen.getByLabelText("Value for MIXED")).toHaveValue("A\nB\nC\n");
    expect(screen.getByLabelText("Service for MIXED")).toHaveValue("api worker");
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
    ]);
    expect(within(table).getByRole("textbox", { name: "DATABASE_URL 的存储值" }).textContent).toBe(" 原始业务值 ");
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
    expect(screen.queryByRole("button", { name: "Edit configuration" })).not.toBeInTheDocument();
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
    const edit = await screen.findByRole("button", { name: "Edit configuration" });

    await user.click(edit);
    await waitFor(() => expect(screen.getByRole("heading", { name: "Edit configuration" })).toHaveFocus());
    await user.type(screen.getByLabelText("Value for EMPTY"), "discard me");
    await user.click(screen.getByRole("button", { name: "Cancel editing" }));
    await user.click(screen.getByRole("button", { name: "Discard and leave" }));
    const restoredEdit = await screen.findByRole("button", { name: "Edit configuration" });
    await waitFor(() => expect(restoredEdit).toHaveFocus());

    await user.click(restoredEdit);
    await waitFor(() => expect(screen.getByRole("heading", { name: "Edit configuration" })).toHaveFocus());
    await user.type(screen.getByLabelText("Value for EMPTY"), "saved exact");
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

    await user.click(await screen.findByRole("button", { name: "Edit configuration" }));
    await waitFor(() => expect(screen.getByRole("heading", { name: "Edit configuration" })).toHaveFocus());
    const value = screen.getByLabelText("Value for PROD_ONLY");
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
});
