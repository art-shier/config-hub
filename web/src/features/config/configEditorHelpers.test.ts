import { describe, expect, it } from "vitest";
import type { ConfigEntry } from "../../api/types";
import { sameSnapshot, type DraftEntry } from "./configEditorHelpers";

function draft(entries: ConfigEntry[]): DraftEntry[] {
  return entries.map((entry, index) => ({ ...entry, id: `draft-${index}` }));
}

describe("sameSnapshot", () => {
  it("treats unique normalized entries as equal regardless of order", () => {
    expect(sameSnapshot(
      draft([
        { key: " SECOND ", value: "two", service: " worker " },
        { key: "FIRST", value: "one", service: "api" },
      ]),
      [
        { key: "FIRST", value: "one", service: "api" },
        { key: "SECOND", value: "two", service: "worker" },
      ],
    )).toBe(true);
  });

  it.each([
    {
      name: "the same order",
      entries: [
        { key: "DUPLICATE", value: "one", service: "api" },
        { key: "DUPLICATE", value: "two", service: "worker" },
      ],
    },
    {
      name: "a different order",
      entries: [
        { key: "DUPLICATE", value: "two", service: "worker" },
        { key: "DUPLICATE", value: "one", service: "api" },
      ],
    },
  ])("treats the same duplicate multiset in $name as unequal", ({ entries }) => {
    expect(sameSnapshot(
      draft([
        { key: " DUPLICATE ", value: "one", service: " api " },
        { key: "DUPLICATE", value: "two", service: "worker" },
      ]),
      entries,
    )).toBe(false);
  });

  it("treats a duplicate on either side as unequal", () => {
    const unique = [
      { key: "FIRST", value: "one", service: "api" },
      { key: "SECOND", value: "two", service: "worker" },
    ];
    const duplicates = [
      { key: "FIRST", value: "one", service: "api" },
      { key: "FIRST", value: "two", service: "worker" },
    ];

    expect(sameSnapshot(draft(duplicates), unique)).toBe(false);
    expect(sameSnapshot(draft(unique), duplicates)).toBe(false);
  });
});
