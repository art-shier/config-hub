import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ExactValue } from "./ExactValue";

describe("ExactValue", () => {
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
