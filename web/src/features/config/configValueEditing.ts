export interface TextareaEditMetadata {
  inputType: string;
  selectionStart: number;
  selectionEnd: number;
  nextSelectionStart: number;
  nextSelectionEnd: number;
}

export type TextareaValueEditResult =
  | { kind: "applied"; value: string }
  | { kind: "unsupported"; value: string };

interface DisplayReplacement {
  start: number;
  end: number;
  nextEnd: number;
}

export function toTextareaDisplayValue(rawValue: string): string {
  return rawValue.replace(/\r\n?/gu, "\n");
}

export function applyTextareaValueEdit(
  rawValue: string,
  nextDisplayValue: string,
  metadata: TextareaEditMetadata | null,
): TextareaValueEditResult {
  const previousDisplayLength = displayLength(rawValue);
  if (metadata === null) {
    return safeFallback(rawValue, nextDisplayValue);
  }

  const replacement = resolveDisplayReplacement(
    previousDisplayLength,
    nextDisplayValue.length,
    metadata,
  );
  if (replacement === null) {
    return safeFallback(rawValue, nextDisplayValue);
  }

  const rawRange = rawOffsetsForDisplayRange(
    rawValue,
    replacement.start,
    replacement.end,
  );
  if (
    rawRange === null ||
    !rawDisplayRangeMatches(
      rawValue,
      0,
      rawRange.start,
      nextDisplayValue,
      0,
      replacement.start,
    ) ||
    !rawDisplayRangeMatches(
      rawValue,
      rawRange.end,
      rawValue.length,
      nextDisplayValue,
      replacement.nextEnd,
      nextDisplayValue.length,
    )
  ) {
    return safeFallback(rawValue, nextDisplayValue);
  }

  const prefix = rawValue.slice(0, rawRange.start);
  const inserted = nextDisplayValue.slice(replacement.start, replacement.nextEnd);
  const suffix = rawValue.slice(rawRange.end);
  const separatorGuard =
    prefix.endsWith("\r") && (inserted + suffix).startsWith("\n") ? "\r" : "";
  return {
    kind: "applied",
    value: prefix + separatorGuard + inserted + suffix,
  };
}

function resolveDisplayReplacement(
  previousLength: number,
  nextLength: number,
  metadata: TextareaEditMetadata,
): DisplayReplacement | null {
  const {
    inputType,
    nextSelectionEnd,
    nextSelectionStart,
    selectionEnd,
    selectionStart,
  } = metadata;
  if (
    !validRange(selectionStart, selectionEnd, previousLength) ||
    !validRange(nextSelectionStart, nextSelectionEnd, nextLength)
  ) {
    return null;
  }

  if (inputType.startsWith("insert")) {
    const insertedLength =
      nextLength - (previousLength - (selectionEnd - selectionStart));
    const nextEnd = selectionStart + insertedLength;
    if (
      insertedLength < 0 ||
      nextSelectionStart !== nextEnd ||
      nextSelectionEnd !== nextEnd
    ) {
      return null;
    }
    return { start: selectionStart, end: selectionEnd, nextEnd };
  }

  if (!inputType.startsWith("delete") || nextSelectionStart !== nextSelectionEnd) {
    return null;
  }
  if (selectionStart !== selectionEnd) {
    if (
      nextLength !== previousLength - (selectionEnd - selectionStart) ||
      nextSelectionStart !== selectionStart
    ) {
      return null;
    }
    return {
      start: selectionStart,
      end: selectionEnd,
      nextEnd: selectionStart,
    };
  }

  const deletedLength = previousLength - nextLength;
  if (deletedLength <= 0) {
    return null;
  }
  if (nextSelectionStart === selectionStart - deletedLength) {
    return {
      start: nextSelectionStart,
      end: selectionStart,
      nextEnd: nextSelectionStart,
    };
  }
  if (nextSelectionStart === selectionStart) {
    return {
      start: selectionStart,
      end: selectionStart + deletedLength,
      nextEnd: selectionStart,
    };
  }
  return null;
}

function validRange(start: number, end: number, length: number): boolean {
  return Number.isInteger(start) &&
    Number.isInteger(end) &&
    start >= 0 &&
    start <= end &&
    end <= length;
}

function displayLength(rawValue: string): number {
  let length = 0;
  for (let rawIndex = 0; rawIndex < rawValue.length; length += 1) {
    rawIndex +=
      rawValue[rawIndex] === "\r" && rawValue[rawIndex + 1] === "\n" ? 2 : 1;
  }
  return length;
}

function rawOffsetsForDisplayRange(
  rawValue: string,
  displayStart: number,
  displayEnd: number,
): { start: number; end: number } | null {
  let rawStart = displayStart === 0 ? 0 : -1;
  let rawEnd = displayEnd === 0 ? 0 : -1;
  let displayIndex = 0;
  let rawIndex = 0;
  while (rawIndex < rawValue.length && (rawStart < 0 || rawEnd < 0)) {
    rawIndex +=
      rawValue[rawIndex] === "\r" && rawValue[rawIndex + 1] === "\n" ? 2 : 1;
    displayIndex += 1;
    if (displayIndex === displayStart) {
      rawStart = rawIndex;
    }
    if (displayIndex === displayEnd) {
      rawEnd = rawIndex;
    }
  }
  if (rawStart < 0 || rawEnd < 0) {
    return null;
  }
  return { start: rawStart, end: rawEnd };
}

function rawDisplayRangeMatches(
  rawValue: string,
  rawStart: number,
  rawEnd: number,
  displayValue: string,
  displayStart: number,
  displayEnd: number,
): boolean {
  let displayIndex = displayStart;
  let rawIndex = rawStart;
  while (rawIndex < rawEnd) {
    const isCarriageReturn = rawValue[rawIndex] === "\r";
    const displayCharacter = isCarriageReturn ? "\n" : rawValue[rawIndex];
    if (
      displayIndex >= displayEnd ||
      displayValue[displayIndex] !== displayCharacter
    ) {
      return false;
    }
    rawIndex +=
      isCarriageReturn && rawValue[rawIndex + 1] === "\n" ? 2 : 1;
    displayIndex += 1;
  }
  return displayIndex === displayEnd;
}

function safeFallback(
  rawValue: string,
  nextDisplayValue: string,
): TextareaValueEditResult {
  if (!rawValue.includes("\r")) {
    return { kind: "applied", value: nextDisplayValue };
  }
  if (rawDisplayRangeMatches(
    rawValue,
    0,
    rawValue.length,
    nextDisplayValue,
    0,
    nextDisplayValue.length,
  )) {
    return { kind: "applied", value: rawValue };
  }
  return { kind: "unsupported", value: rawValue };
}
