import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StrictMode, useState } from "react";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { APIClientContract } from "../../api/types";
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

  it("filters current entries by key or service without changing values", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revision: {
        id: "revision-2",
        environment_id: "env-prod",
        message: "two",
        created_by: "user-admin",
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
    await user.clear(screen.getByRole("searchbox", { name: "Search configuration" }));
    await user.type(screen.getByRole("searchbox", { name: "Search configuration" }), "database");
    expect(screen.getByTestId("configuration-value-DATABASE_URL").textContent).toBe(" postgres://exact ");
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
        version: 9,
        created_at: "2026-08-29T08:00:00Z",
        entries: [{ key: "STALE_PROD", value: "prod", service: "" }],
      },
    });
    await waitFor(() => expect(screen.queryByText("STALE_PROD")).not.toBeInTheDocument());
    expect(screen.getByText("STAGE_ONLY")).toBeInTheDocument();
  });
});
