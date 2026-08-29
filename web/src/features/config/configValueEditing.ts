export function toTextareaDisplayValue(rawValue: string): string {
  return rawValue.replace(/\r\n?/gu, "\n");
}

export function applyTextareaValueEdit(rawValue: string, nextDisplayValue: string): string {
  const previousDisplayValue = toTextareaDisplayValue(rawValue);
  let prefixLength = 0;
  while (
    prefixLength < previousDisplayValue.length &&
    prefixLength < nextDisplayValue.length &&
    previousDisplayValue[prefixLength] === nextDisplayValue[prefixLength]
  ) {
    prefixLength += 1;
  }

  let suffixLength = 0;
  while (
    suffixLength < previousDisplayValue.length - prefixLength &&
    suffixLength < nextDisplayValue.length - prefixLength &&
    previousDisplayValue[previousDisplayValue.length - suffixLength - 1] ===
      nextDisplayValue[nextDisplayValue.length - suffixLength - 1]
  ) {
    suffixLength += 1;
  }

  const rawBoundaries = displayBoundariesInRawValue(rawValue);
  const rawPrefixEnd = rawBoundaries[prefixLength] ?? rawValue.length;
  const rawSuffixStart = rawBoundaries[previousDisplayValue.length - suffixLength] ?? rawValue.length;
  const changedDisplayEnd = nextDisplayValue.length - suffixLength;
  return rawValue.slice(0, rawPrefixEnd) +
    nextDisplayValue.slice(prefixLength, changedDisplayEnd) +
    rawValue.slice(rawSuffixStart);
}

function displayBoundariesInRawValue(rawValue: string): number[] {
  const boundaries = [0];
  let rawIndex = 0;
  while (rawIndex < rawValue.length) {
    rawIndex += rawValue[rawIndex] === "\r" && rawValue[rawIndex + 1] === "\n" ? 2 : 1;
    boundaries.push(rawIndex);
  }
  return boundaries;
}
