import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StrictMode } from "react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { APIError } from "../../api/client";
import type { APIClientContract } from "../../api/types";
import { changeLocale } from "../../i18n";
import { VersionList } from "./VersionList";

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

describe("VersionList", () => {
  it("does not load history before an environment is available", () => {
    const api = client();
    render(
      <VersionList
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

  it("loads encoded history and renders revision metadata", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revisions: [
        {
          id: "revision-2",
          environment_id: "env-prod",
          version: 2,
          message: "发布 中文 😀",
          created_by: "ada",
          created_at: "2026-08-29T08:00:00Z",
        },
      ],
    });
    render(
      <StrictMode>
        <VersionList
          client={api}
          projectSlug="shop/intl"
          environmentSlug="prod west"
          canWrite
          refreshEpoch={0}
          onRevisionChanged={vi.fn()}
        />
      </StrictMode>,
    );

    expect(screen.getByRole("status")).toHaveTextContent("Loading version history");
    const item = await screen.findByRole("article", { name: "Version 2" });
    expect(within(item).getByText("发布 中文 😀")).toBeInTheDocument();
    expect(within(item).getByText("ada")).toBeInTheDocument();
    expect(within(item).getByText(/2026/u)).toBeInTheDocument();
    await waitFor(() => expect(api.get).toHaveBeenCalledWith(
      "/projects/shop%2Fintl/environments/prod%20west/revisions",
    ));
  });

  it("shows Chinese version actions while preserving revision messages and uses the active date locale", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revisions: [{
        id: "revision-1",
        environment_id: "env-prod",
        version: 1,
        message: "发布 中文 🍾",
        created_by: "ada",
        created_at: "2026-08-29T08:00:00Z",
      }],
    });
    const dateTimeFormat = vi.spyOn(Intl, "DateTimeFormat");
    await act(async () => {
      await changeLocale("zh-CN");
    });

    render(
      <VersionList
        client={api}
        projectSlug="shop"
        environmentSlug="prod"
        canWrite
        refreshEpoch={0}
        onRevisionChanged={vi.fn()}
      />,
    );

    expect(await screen.findByRole("heading", { name: "版本" })).toBeVisible();
    expect(screen.getByText("发布 中文 🍾")).toBeVisible();
    expect(screen.getByRole("button", { name: "查看版本 1" })).toBeVisible();
    expect(dateTimeFormat).toHaveBeenCalledWith(
      "zh-CN",
      expect.objectContaining({ dateStyle: "medium", timeStyle: "short" }),
    );
  });

  it("uses a local rollback validation message without disclosing server fields", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revisions: [{
        id: "revision-1",
        environment_id: "env-prod",
        version: 1,
        message: "发布 中文 🍾",
        created_by: "ada",
        created_at: "2026-08-29T08:00:00Z",
      }],
    });
    vi.mocked(api.post).mockRejectedValue(
      new APIError(422, "validation_failed", "RAW SECRET", "req", { message: "RAW FIELD" }),
    );
    await act(async () => {
      await changeLocale("zh-CN");
    });
    render(
      <VersionList
        client={api}
        projectSlug="shop"
        environmentSlug="prod"
        canWrite
        refreshEpoch={0}
        onRevisionChanged={vi.fn()}
      />,
    );
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "回滚到版本 1" }));
    await user.click(screen.getByRole("button", { name: "创建回滚版本" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("请输入有效的回滚说明。");
    expect(screen.queryByText(/RAW/u)).not.toBeInTheDocument();
  });

  it("retranslates a rollback validation error when the active locale changes", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revisions: [{
        id: "revision-1",
        environment_id: "env-prod",
        version: 1,
        message: "发布 中文 🍾",
        created_by: "ada",
        created_at: "2026-08-29T08:00:00Z",
      }],
    });
    vi.mocked(api.post).mockRejectedValue(
      new APIError(422, "validation_failed", "RAW SECRET", "req", { message: "RAW FIELD" }),
    );
    await act(async () => {
      await changeLocale("zh-CN");
    });
    render(
      <VersionList
        client={api}
        projectSlug="shop"
        environmentSlug="prod"
        canWrite
        refreshEpoch={0}
        onRevisionChanged={vi.fn()}
      />,
    );
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "回滚到版本 1" }));
    await user.click(screen.getByRole("button", { name: "创建回滚版本" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("请输入有效的回滚说明。");

    await act(async () => {
      await changeLocale("en-US");
    });

    expect(screen.getByRole("alert")).toHaveTextContent("Enter a valid rollback message.");
    expect(screen.queryByText(/RAW/u)).not.toBeInTheDocument();
  });

  it("shows empty history after a focusable retry", async () => {
    const api = client();
    vi.mocked(api.get)
      .mockRejectedValueOnce(new Error("SECRET history"))
      .mockResolvedValueOnce({ revisions: [] });
    render(
      <VersionList
        client={api}
        projectSlug="shop"
        environmentSlug="prod"
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
    expect(await screen.findByRole("heading", { name: "No versions yet" })).toBeInTheDocument();
  });

  it("loads version detail and a full selected-to-current diff with absent distinct from empty", async () => {
    const api = client();
    vi.mocked(api.get).mockImplementation((path) => {
      if (path.endsWith("/revisions")) {
        return Promise.resolve({
          revisions: [
            { id: "r2", environment_id: "env", version: 2, message: "current", created_by: "ada", created_at: "2026-08-29T08:00:00Z" },
            { id: "r1", environment_id: "env", version: 1, message: "first", created_by: "lee", created_at: "2026-08-28T08:00:00Z" },
          ],
        });
      }
      if (path.endsWith("/1/diff")) {
        return Promise.resolve({
          before_revision: 1,
          after_revision: 2,
          changes: [
            { key: "ADDED_EMPTY", kind: "added", before: "", after: "", before_service: "", after_service: "api" },
            { key: "CHANGED", kind: "changed", before: "before  ", after: "after\n完整 😀", before_service: "api", after_service: "worker" },
            { key: "DELETED_EMPTY", kind: "deleted", before: "", after: "", before_service: "", after_service: "" },
          ],
        });
      }
      return Promise.resolve({
        revision: {
          id: "r1",
          environment_id: "env",
          version: 1,
          message: "first",
          created_by: "lee",
          created_at: "2026-08-28T08:00:00Z",
          entries: [{ key: "CHANGED", value: "before  ", service: "api" }],
        },
      });
    });
    render(
      <VersionList
        client={api}
        projectSlug="shop"
        environmentSlug="prod"
        canWrite
        refreshEpoch={0}
        onRevisionChanged={vi.fn()}
      />,
    );
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "View version 1" }));

    expect(await screen.findByRole("heading", { name: "Version 1 to current version 2" })).toBeInTheDocument();
    expect(screen.getByText("first", { selector: ".selected-revision-message" })).toBeInTheDocument();
    expect(screen.getByTestId("diff-before-CHANGED").textContent).toBe("before  ");
    expect(screen.getByTestId("diff-after-CHANGED").textContent).toBe("after\n完整 😀");
    expect(screen.getByTestId("diff-before-service-CHANGED").textContent).toContain("api");
    expect(screen.getByTestId("diff-after-service-CHANGED").textContent).toContain("worker");
    expect(screen.getByTestId("diff-before-ADDED_EMPTY")).toHaveTextContent("Absent");
    expect(screen.getByRole("textbox", { name: "Current version value for ADDED_EMPTY" }).textContent).toBe("");
    expect(screen.getByRole("textbox", { name: "Selected version value for DELETED_EMPTY" }).textContent).toBe("");
    expect(screen.getByTestId("diff-after-DELETED_EMPTY")).toHaveTextContent("Absent");
    expect(api.get).toHaveBeenCalledWith("/projects/shop/environments/prod/revisions/1");
    expect(api.get).toHaveBeenCalledWith("/projects/shop/environments/prod/revisions/1/diff");
  });

  it("confirms rollback creates a new version, prevents double submit, and delegates refresh", async () => {
    let resolveRollback!: (value: unknown) => void;
    const api = client();
    const onRevisionChanged = vi.fn();
    vi.mocked(api.get).mockResolvedValue({
      revisions: [{ id: "r1", environment_id: "env", version: 1, message: "first", created_by: "lee", created_at: "2026-08-28T08:00:00Z" }],
    });
    vi.mocked(api.post).mockReturnValue(new Promise<never>((resolve) => {
      resolveRollback = resolve as (value: unknown) => void;
    }));
    render(
      <VersionList
        client={api}
        projectSlug="shop"
        environmentSlug="prod"
        canWrite
        refreshEpoch={0}
        onRevisionChanged={onRevisionChanged}
      />,
    );
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Rollback to version 1" }));
    const dialog = screen.getByRole("dialog", { name: "Rollback to version 1?" });
    expect(within(dialog).getByText(/rollback creates a new current version/iu)).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Rollback message"), "restore known values");
    await user.dblClick(within(dialog).getByRole("button", { name: "Create rollback version" }));

    expect(api.post).toHaveBeenCalledTimes(1);
    expect(api.post).toHaveBeenCalledWith(
      "/projects/shop/environments/prod/revisions/1/rollback",
      { message: "restore known values" },
    );
    expect(within(dialog).getByRole("button", { name: "Creating rollback…" })).toBeDisabled();
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(screen.getByRole("dialog", { name: "Rollback to version 1?" })).toBeInTheDocument();
    resolveRollback({
      revision: {
        id: "r3", environment_id: "env", version: 3, message: "restore known values", created_by: "ada", created_at: "2026-08-29T09:00:00Z", entries: [],
      },
    });
    await waitFor(() => expect(onRevisionChanged).toHaveBeenCalledTimes(1));
    expect(api.get).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("fully resets rollback state when the shared refresh epoch changes during success", async () => {
    let resolveRefresh!: (value: unknown) => void;
    let historyRequests = 0;
    const api = client();
    const history = {
      revisions: [{ id: "r1", environment_id: "env", version: 1, message: "first", created_by: "lee", created_at: "2026-08-28T08:00:00Z" }],
    };
    vi.mocked(api.get).mockImplementation(() => {
      historyRequests += 1;
      if (historyRequests === 1) return Promise.resolve(history);
      return new Promise<never>((resolve) => {
        resolveRefresh = resolve as (value: unknown) => void;
      });
    });
    vi.mocked(api.post).mockResolvedValue({
      revision: { id: "r2", environment_id: "env", version: 2, message: "restore", created_by: "ada", created_at: "2026-08-29T09:00:00Z", entries: [] },
    });
    function Harness() {
      const [epoch, setEpoch] = useState(0);
      return (
        <VersionList
          client={api}
          projectSlug="shop"
          environmentSlug="prod"
          canWrite
          refreshEpoch={epoch}
          onRevisionChanged={() => setEpoch((current) => current + 1)}
        />
      );
    }
    render(<Harness />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Rollback to version 1" }));
    await user.click(screen.getByRole("button", { name: "Create rollback version" }));
    await waitFor(() => expect(historyRequests).toBeGreaterThan(1));
    resolveRefresh(history);
    await user.click(await screen.findByRole("button", { name: "Rollback to version 1" }));

    expect(screen.getByRole("button", { name: "Create rollback version" })).toBeEnabled();
  });

  it("refreshes history exactly once through the shared epoch after rollback succeeds", async () => {
    let historyRequests = 0;
    const api = client();
    vi.mocked(api.get).mockImplementation(() => {
      historyRequests += 1;
      return Promise.resolve({
        revisions: [{ id: "r1", environment_id: "env", version: 1, message: "first", created_by: "lee", created_at: "2026-08-28T08:00:00Z" }],
      });
    });
    vi.mocked(api.post).mockResolvedValue({
      revision: { id: "r2", environment_id: "env", version: 2, message: "restore", created_by: "ada", created_at: "2026-08-29T09:00:00Z", entries: [] },
    });
    function Harness() {
      const [epoch, setEpoch] = useState(0);
      return (
        <>
          <output aria-label="Refresh epoch">{epoch}</output>
          <VersionList
            client={api}
            projectSlug="shop"
            environmentSlug="prod"
            canWrite
            refreshEpoch={epoch}
            onRevisionChanged={() => setEpoch((current) => current + 1)}
          />
        </>
      );
    }
    render(<Harness />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Rollback to version 1" }));
    await user.click(screen.getByRole("button", { name: "Create rollback version" }));

    await waitFor(() => expect(screen.getByLabelText("Refresh epoch")).toHaveTextContent("1"));
    await waitFor(() => expect(historyRequests).toBe(2));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it.each([
    { scope: "environment", nextProject: "shop", nextEnvironment: "stage" },
    { scope: "project", nextProject: "catalog", nextEnvironment: "prod" },
  ])("ends an obsolete pending rollback after a $scope switch without unlocking the new operation", async ({
    nextEnvironment,
    nextProject,
  }) => {
    let rejectOldRollback!: (reason?: unknown) => void;
    let resolveNewRollback!: (value: unknown) => void;
    let oldRollbackSettled = false;
    const oldRollback = new Promise<never>((_resolve, reject) => {
      rejectOldRollback = reject;
    });
    void oldRollback.catch(() => {
      oldRollbackSettled = true;
    });
    const newRollback = new Promise<never>((resolve) => {
      resolveNewRollback = resolve as (value: unknown) => void;
    });
    const api = client();
    const onRevisionChanged = vi.fn();
    const nextListPath = `/projects/${nextProject}/environments/${nextEnvironment}/revisions`;
    const oldRollbackPath = "/projects/shop/environments/prod/revisions/1/rollback";
    const newRollbackPath = `${nextListPath}/2/rollback`;
    vi.mocked(api.get).mockImplementation((path) => Promise.resolve({
      revisions: path === nextListPath
        ? [{ id: "next-r2", environment_id: "next-env", version: 2, message: "NEXT SCOPE", created_by: "lee", created_at: "2026-08-29T09:00:00Z" }]
        : [{ id: "old-r1", environment_id: "old-env", version: 1, message: "OLD SCOPE", created_by: "ada", created_at: "2026-08-29T08:00:00Z" }],
    }));
    vi.mocked(api.post).mockImplementation((path) =>
      path === oldRollbackPath ? oldRollback : newRollback,
    );
    const renderVersionList = (projectSlug: string, environmentSlug: string) => (
      <StrictMode>
        <VersionList
          client={api}
          projectSlug={projectSlug}
          environmentSlug={environmentSlug}
          canWrite
          refreshEpoch={0}
          onRevisionChanged={onRevisionChanged}
        />
      </StrictMode>
    );
    const view = render(renderVersionList("shop", "prod"));
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Rollback to version 1" }));
    await user.click(screen.getByRole("button", { name: "Create rollback version" }));
    expect(screen.getByRole("button", { name: "Creating rollback…" })).toBeDisabled();

    view.rerender(renderVersionList(nextProject, nextEnvironment));
    expect(await screen.findByText("NEXT SCOPE")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Rollback to version 2" }));
    const nextSubmit = screen.getByRole("button", { name: "Create rollback version" });
    expect(nextSubmit).toBeEnabled();
    await user.click(nextSubmit);
    expect(screen.getByRole("button", { name: "Creating rollback…" })).toBeDisabled();

    rejectOldRollback(new APIError(503, "service_unavailable", "OLD SCOPE SECRET", "req", {}));
    await waitFor(() => expect(oldRollbackSettled).toBe(true));
    expect(screen.getByRole("button", { name: "Creating rollback…" })).toBeDisabled();
    expect(screen.queryByText("OLD SCOPE SECRET")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    resolveNewRollback({
      revision: { id: "next-r3", environment_id: "next-env", version: 3, message: "restore next", created_by: "ada", created_at: "2026-08-29T10:00:00Z", entries: [] },
    });
    await waitFor(() => expect(onRevisionChanged).toHaveBeenCalledTimes(1));
    expect(onRevisionChanged).toHaveBeenCalledWith(expect.objectContaining({ id: "next-r3" }));
    expect(api.post).toHaveBeenCalledWith(oldRollbackPath, { message: "" });
    expect(api.post).toHaveBeenCalledWith(newRollbackPath, { message: "" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("retains rollback message after a safe typed failure and hides write entry points from viewers", async () => {
    const api = client();
    vi.mocked(api.get).mockResolvedValue({
      revisions: [{ id: "r1", environment_id: "env", version: 1, message: "first", created_by: "lee", created_at: "2026-08-28T08:00:00Z" }],
    });
    vi.mocked(api.post).mockRejectedValue(new APIError(503, "service_unavailable", "SECRET", "req", {}));
    const view = render(
      <VersionList
        client={api}
        projectSlug="shop"
        environmentSlug="prod"
        canWrite
        refreshEpoch={0}
        onRevisionChanged={vi.fn()}
      />,
    );
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Rollback to version 1" }));
    await user.type(screen.getByLabelText("Rollback message"), "keep this message");
    await user.click(screen.getByRole("button", { name: "Create rollback version" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("couldn’t create the rollback version");
    expect(screen.getByLabelText("Rollback message")).toHaveValue("keep this message");
    expect(screen.queryByText("SECRET")).not.toBeInTheDocument();

    view.rerender(
      <VersionList
        client={api}
        projectSlug="shop"
        environmentSlug="prod"
        canWrite={false}
        refreshEpoch={0}
        onRevisionChanged={vi.fn()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("button", { name: /Rollback to version/u })).not.toBeInTheDocument();
  });

  it("discards stale list responses when the environment changes", async () => {
    let resolveProd!: (value: unknown) => void;
    const api = client();
    vi.mocked(api.get).mockImplementation((path) => {
      if (path.includes("/prod/")) return new Promise<never>((resolve) => {
        resolveProd = resolve as (value: unknown) => void;
      });
      return Promise.resolve({ revisions: [{ id: "stage", environment_id: "stage", version: 4, message: "STAGE HISTORY", created_by: "lee", created_at: "2026-08-29T08:00:00Z" }] });
    });
    const props = { client: api, projectSlug: "shop", canWrite: true, refreshEpoch: 0, onRevisionChanged: vi.fn() };
    const view = render(<VersionList {...props} environmentSlug="prod" />);
    view.rerender(<VersionList {...props} environmentSlug="stage" />);
    expect(await screen.findByText("STAGE HISTORY")).toBeInTheDocument();
    resolveProd({ revisions: [{ id: "prod", environment_id: "prod", version: 9, message: "STALE PROD", created_by: "ada", created_at: "2026-08-29T08:00:00Z" }] });
    await waitFor(() => expect(screen.queryByText("STALE PROD")).not.toBeInTheDocument());
  });
});
