import { describe, expect, it } from "vitest";
import { applyTextareaValueEdit, toTextareaDisplayValue } from "./configValueEditing";

function applyEdit(
  rawValue: string,
  nextDisplayValue: string,
  {
    inputType,
    selectionEnd,
    selectionStart,
    nextSelectionEnd,
    nextSelectionStart,
  }: {
    inputType: string;
    selectionStart: number;
    selectionEnd: number;
    nextSelectionStart: number;
    nextSelectionEnd?: number;
  },
): string {
  const result = applyTextareaValueEdit(rawValue, nextDisplayValue, {
    inputType,
    selectionStart,
    selectionEnd,
    nextSelectionStart,
    nextSelectionEnd: nextSelectionEnd ?? nextSelectionStart,
  });
  expect(result.kind).toBe("applied");
  return result.value;
}

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
      selectionStart: 0,
      nextSelectionStart: 1,
    },
    {
      name: "in the middle of a CRLF value",
      raw: "A\r\nB",
      nextDisplay: "A!\nB",
      expected: "A!\r\nB",
      selectionStart: 1,
      nextSelectionStart: 2,
    },
    {
      name: "after a mixed-line-ending value",
      raw: "A\rB\r\nC\nD",
      nextDisplay: "A\nB\nC\nD<",
      expected: "A\rB\r\nC\nD<",
      selectionStart: 7,
      nextSelectionStart: 8,
    },
  ])("preserves untouched raw separators when typing $name", ({
    expected,
    nextDisplay,
    nextSelectionStart,
    raw,
    selectionStart,
  }) => {
    expect(applyEdit(raw, nextDisplay, {
      inputType: "insertText",
      selectionStart,
      selectionEnd: selectionStart,
      nextSelectionStart,
    })).toBe(expected);
  });

  it("deletes the selected lone CR before an adjacent CRLF", () => {
    let raw = "A\rB\r\r\nC\nD";
    raw = applyEdit(raw, ">A\nB\n\nC\nD", {
      inputType: "insertText",
      selectionStart: 0,
      selectionEnd: 0,
      nextSelectionStart: 1,
    });
    raw = applyEdit(raw, ">A\nB!\n\nC\nD", {
      inputType: "insertText",
      selectionStart: 4,
      selectionEnd: 4,
      nextSelectionStart: 5,
    });
    raw = applyEdit(raw, ">A\nB!\n\nC\nD<", {
      inputType: "insertText",
      selectionStart: 10,
      selectionEnd: 10,
      nextSelectionStart: 11,
    });

    expect(applyEdit(raw, ">A\nB!\nC\nD<", {
      inputType: "deleteContentForward",
      selectionStart: 5,
      selectionEnd: 5,
      nextSelectionStart: 5,
    })).toBe(">A\rB!\r\nC\nD<");
  });

  it("backspaces the adjacent CRLF without changing the preceding lone CR", () => {
    expect(applyEdit("A\rB\r\r\nC", "A\nB\nC", {
      inputType: "deleteContentBackward",
      selectionStart: 5,
      selectionEnd: 5,
      nextSelectionStart: 4,
    })).toBe("A\rB\rC");
  });

  it("replaces a selection while preserving separators outside it", () => {
    expect(applyEdit("A\rB\r\nC", "A\n值\nC", {
      inputType: "insertText",
      selectionStart: 2,
      selectionEnd: 3,
      nextSelectionStart: 3,
    })).toBe("A\r值\r\nC");
  });

  it("deletes a selected range and only removes separators inside that range", () => {
    expect(applyEdit("A\rB\r\nC\nD", "A\nC\nD", {
      inputType: "deleteContentForward",
      selectionStart: 2,
      selectionEnd: 4,
      nextSelectionStart: 2,
    })).toBe("A\rC\nD");
  });

  it("applies paste and cut using their actual selected display ranges", () => {
    expect(applyEdit("A\rB\r\nC", "A\n值\nx\nC", {
      inputType: "insertFromPaste",
      selectionStart: 2,
      selectionEnd: 3,
      nextSelectionStart: 5,
    })).toBe("A\r值\nx\r\nC");

    expect(applyEdit("A\rB\r\nC", "A\n\nC", {
      inputType: "deleteByCut",
      selectionStart: 2,
      selectionEnd: 3,
      nextSelectionStart: 2,
    })).toBe("A\r\r\nC");
  });

  it("keeps separate neighboring CR and LF separators after deleting between them", () => {
    const result = applyEdit("A\rB\nC", "A\n\nC", {
      inputType: "deleteByCut",
      selectionStart: 2,
      selectionEnd: 3,
      nextSelectionStart: 2,
    });

    expect(result).toBe("A\r\r\nC");
    expect(toTextareaDisplayValue(result)).toBe("A\n\nC");
  });

  it("uses a conservative fallback when explicit edit metadata is unavailable", () => {
    expect(applyTextareaValueEdit("A\rB", "A\n!B", null)).toEqual({
      kind: "unsupported",
      value: "A\rB",
    });
    expect(applyTextareaValueEdit("A\rB", "A\nB", null)).toEqual({
      kind: "applied",
      value: "A\rB",
    });
    expect(applyTextareaValueEdit("A\nB", "A\n!B", null)).toEqual({
      kind: "applied",
      value: "A\n!B",
    });
  });

  it("uses consistent selection metadata for any insert intent, including composition", () => {
    expect(applyEdit("A\rB", "A\n😀B", {
      inputType: "insertCompositionText",
      selectionStart: 2,
      selectionEnd: 2,
      nextSelectionStart: 4,
    })).toBe("A\r😀B");
  });

  it("rejects stale or unsupported metadata instead of guessing an edit range", () => {
    expect(applyTextareaValueEdit("A\rB", "A\n!B", {
      inputType: "insertText",
      selectionStart: 2,
      selectionEnd: 2,
      nextSelectionStart: 2,
      nextSelectionEnd: 2,
    })).toEqual({
      kind: "unsupported",
      value: "A\rB",
    });
    expect(applyTextareaValueEdit("A\rB", "B\nA", {
      inputType: "historyUndo",
      selectionStart: 0,
      selectionEnd: 3,
      nextSelectionStart: 3,
      nextSelectionEnd: 3,
    })).toEqual({
      kind: "unsupported",
      value: "A\rB",
    });
  });

  it("maps a 1 MiB edit without a boundary entry for every display character", () => {
    const raw = "x".repeat(1024 * 1024 - 2) + "\r\n";
    const nextDisplay = "x".repeat(1024 * 1024 - 2) + "!\n";

    expect(applyEdit(raw, nextDisplay, {
      inputType: "insertText",
      selectionStart: 1024 * 1024 - 2,
      selectionEnd: 1024 * 1024 - 2,
      nextSelectionStart: 1024 * 1024 - 1,
    })).toBe("x".repeat(1024 * 1024 - 2) + "!\r\n");
  });
});
