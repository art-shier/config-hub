import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ExactValue } from "./ExactValue";
import { changeLocale } from "../i18n";

describe("ExactValue", () => {
  // Break caught: leaving the shared empty-value marker in the previous UI language.
  it("localizes the empty-string marker without translating stored data", async () => {
    await changeLocale("zh-CN");
    render(<ExactValue label="Stored value for EMPTY" value="" />);

    expect(screen.getByText("\u7a7a\u5b57\u7b26\u4e32")).toBeVisible();
    expect(screen.getByRole("textbox", { name: "Stored value for EMPTY" }).textContent).toBe("");
  });
  it("keeps every character in a focusable read-only scrolling control", () => {
    const value = `  第一行 😀\n${"x".repeat(30_000)}  `;
    render(<ExactValue label="Stored value for EXTREME" value={value} />);

    const control = screen.getByRole("textbox", { name: "Stored value for EXTREME" });
    expect(control.textContent).toBe(value);
    expect(control).toHaveAttribute("aria-readonly", "true");
    expect(control).toHaveAttribute("tabindex", "0");
  });

  it("labels an exact empty string without putting placeholder content in the value", () => {
    render(<ExactValue label="Stored value for EMPTY" value="" />);

    expect(screen.getByText("Empty string")).toBeVisible();
    expect(screen.getByRole("textbox", { name: "Stored value for EMPTY" }).textContent).toBe("");
  });

  it("preserves carriage-return and CRLF code units", () => {
    const value = "first\rsecond\r\nthird\nfourth";
    render(<ExactValue label="Stored value for LINE_ENDINGS" value={value} />);

    const control = screen.getByRole("textbox", { name: "Stored value for LINE_ENDINGS" });
    const renderedValue = control instanceof HTMLTextAreaElement ? control.value : control.textContent;
    expect(renderedValue).toBe(value);
    expect(control.textContent).toBe(value);
  });
});
