import { describe, expect, it } from "vitest";
import type { ConfigEntry } from "../../api/types";
import {
  mapServerValidation,
  sameSnapshot,
  toDraftEntry,
  validateEntries,
  type DraftEntry,
} from "./configEditorHelpers";

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

describe("localized validation boundaries", () => {
  it("uses caller-supplied client validation messages", () => {
    const invalid = toDraftEntry({ key: "invalid key", value: "", service: "" });
    const duplicate = toDraftEntry({ key: "invalid key", value: "", service: "" });

    const errors = validateEntries(
      [invalid, duplicate],
      { invalidKey: "INVALID_LOCAL", duplicateKey: "DUPLICATE_LOCAL" },
    );

    expect(errors[invalid.id]?.key).toBe("INVALID_LOCAL");
    expect(errors[duplicate.id]?.key).toBe("DUPLICATE_LOCAL");
  });

  it("maps supported server paths to localized messages without returning server values", () => {
    const result = mapServerValidation(
      {
        entries: "RAW ENTRIES SECRET",
        message: "RAW MESSAGE SECRET",
        "entries[0].key": "RAW KEY SECRET",
        "entries[0].value": "RAW VALUE SECRET",
        "entries[0].service": "RAW SERVICE SECRET",
        "entries[3].key": "RAW OUT-OF-RANGE SECRET",
        unexpected: "RAW UNKNOWN SECRET",
      },
      ["row-1"],
      {
        entries: "ENTRIES_LOCAL",
        message: "MESSAGE_LOCAL",
        key: "KEY_LOCAL",
        value: "VALUE_LOCAL",
        service: "SERVICE_LOCAL",
      },
    );

    expect(result).toEqual({
      entriesError: "ENTRIES_LOCAL",
      entryErrors: {
        "row-1": {
          key: "KEY_LOCAL",
          value: "VALUE_LOCAL",
          service: "SERVICE_LOCAL",
        },
      },
      messageError: "MESSAGE_LOCAL",
    });
    expect(JSON.stringify(result)).not.toContain("RAW");
  });
});
