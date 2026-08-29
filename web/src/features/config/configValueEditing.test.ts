import { describe, expect, it } from "vitest";
import { applyTextareaValueEdit, toTextareaDisplayValue } from "./configValueEditing";

describe("configuration textarea value editing", () => {
  it("normalizes raw line endings only for the textarea display", () => {
    expect(toTextareaDisplayValue("A\rB\r\nC\nD")).toBe("A\nB\nC\nD");
  });

  it.each([
    {
      name: "before a CR value",
      raw: "A\rB",
      nextDisplay: ">A\nB",
      expected: ">A\rB",
    },
    {
      name: "in the middle of a CRLF value",
      raw: "A\r\nB",
      nextDisplay: "A!\nB",
      expected: "A!\r\nB",
    },
    {
      name: "after a mixed-line-ending value",
      raw: "A\rB\r\nC\nD",
      nextDisplay: "A\nB\nC\nD<",
      expected: "A\rB\r\nC\nD<",
    },
  ])("preserves untouched raw separators when editing $name", ({ raw, nextDisplay, expected }) => {
    expect(applyTextareaValueEdit(raw, nextDisplay)).toBe(expected);
  });

  it.each([
    {
      name: "CR",
      nextDisplay: "AB\nC\nD",
      expected: "AB\r\nC\nD",
    },
    {
      name: "CRLF",
      nextDisplay: "A\nBC\nD",
      expected: "A\rBC\nD",
    },
    {
      name: "LF",
      nextDisplay: "A\nB\nCD",
      expected: "A\rB\r\nCD",
    },
  ])("deletes only the edited $name separator", ({ nextDisplay, expected }) => {
    expect(applyTextareaValueEdit("A\rB\r\nC\nD", nextDisplay)).toBe(expected);
  });

  it("returns the exact raw value when the normalized display is unchanged", () => {
    expect(applyTextareaValueEdit("A\rB\r\nC", "A\nB\nC")).toBe("A\rB\r\nC");
  });

  it("uses new browser input for inserted separators and modified text", () => {
    expect(applyTextareaValueEdit("A\rB", "A\n!\nB")).toBe("A\r!\nB");
    expect(applyTextareaValueEdit("A\rB\r\nC\nD", "A\nX\nC\nD")).toBe("A\rX\r\nC\nD");
  });
});
