import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../app/App";
import { server } from "../test/setup";

function renderAgentAccess() {
  server.use(
    http.get("/api/v1/auth/session", () => HttpResponse.json({
      user: { id: "member-id", username: "member", display_name: "Member", role: "member" },
      csrf_token: "csrf-member",
      expires_at: "2026-08-30T09:00:00Z",
    })),
  );
  window.history.pushState({}, "", "/agent-access");
  return render(<App />);
}

afterEach(() => vi.restoreAllMocks());

describe("AgentAccessPage", () => {
  it("lets a member copy the site skill URL and a prompt containing CLI setup", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
    renderAgentAccess();

    await screen.findByRole("heading", { name: "Agent Setup" });
    expect(screen.getByRole("link", { name: "Agent Setup" })).toHaveAttribute("aria-current", "page");
    expect(document.title).toBe("ConfigHub — Agent Setup");
    const url = `${window.location.origin}/skills/confighub/SKILL.md`;
    expect(screen.getByRole("textbox", { name: "Skill URL" })).toHaveValue(url);
    expect(screen.getByRole("link", { name: /Read SKILL.md/ })).toHaveAttribute("href", url);

    await user.click(screen.getByRole("button", { name: "Copy URL" }));
    expect(writeText).toHaveBeenLastCalledWith(url);
    expect(screen.getByRole("status")).toHaveTextContent("Copied to clipboard.");

    await user.click(screen.getByRole("button", { name: "Copy prompt" }));
    const prompt = (screen.getByRole("textbox", { name: "Ready-to-use prompt" }) as HTMLTextAreaElement).value;
    expect(writeText).toHaveBeenLastCalledWith(prompt);
    expect(prompt).toContain(url);
    expect(prompt).toContain("installation steps");
    expect(prompt).not.toContain("csrf-member");
  });

  it("keeps text selectable after a clipboard failure and localizes the recovery", async () => {
    const user = userEvent.setup();
    vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("denied"));
    renderAgentAccess();
    await screen.findByRole("heading", { name: "Agent Setup" });
    await user.click(screen.getByRole("button", { name: "Copy URL" }));
    expect(screen.getByRole("alert")).toHaveTextContent("copy it manually");
    const input = screen.getByRole("textbox", { name: "Skill URL" }) as HTMLInputElement;
    await user.click(input);
    expect(input.selectionStart).toBe(0);
    expect(input.selectionEnd).toBe(input.value.length);

    await user.selectOptions(screen.getByRole("combobox", { name: "Language" }), "zh-CN");
    expect(screen.getByRole("heading", { name: "接入 Agent" })).toBeVisible();
    expect(document.title).toBe("ConfigHub — 接入 Agent");
    expect(screen.getByRole("alert")).toHaveTextContent("手动复制");
    expect((screen.getByRole("textbox", { name: "可直接使用的提示词" }) as HTMLTextAreaElement).value).toContain("缺失时按 skill 安装");
  });
});
