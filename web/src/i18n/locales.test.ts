import { describe, expect, it } from "vitest";
import {
  resolvePreferredLocale,
  safeReadLocale,
  safeWriteLocale,
} from "./locales";

describe("locale preferences", () => {
  it("uses a valid stored locale before browser preferences", () => {
    expect(resolvePreferredLocale("en-US", ["zh-CN"])).toBe("en-US");
  });

  it.each([
    [["zh-Hans-CN"], "zh-CN"],
    [["en-GB"], "en-US"],
    [["fr-FR"], "en-US"],
  ] as const)("maps %j to %s", (languages, expected) => {
    expect(resolvePreferredLocale(null, languages)).toBe(expected);
  });

  it("ignores an invalid stored value before considering browser preferences", () => {
    expect(resolvePreferredLocale("fr-FR", ["zh-CN"])).toBe("zh-CN");
  });

  it("survives storage access failures", () => {
    const storage = {
      getItem: () => {
        throw new DOMException("blocked");
      },
    };

    expect(safeReadLocale(storage)).toBeNull();
  });

  it("writes a supported locale without exposing storage failures", () => {
    let written: [string, string] | null = null;
    safeWriteLocale(
      {
        setItem(key, value) {
          written = [key, value];
        },
      },
      "zh-CN",
    );

    expect(written).toEqual(["confighub.locale", "zh-CN"]);
    expect(() =>
      safeWriteLocale(
        {
          setItem() {
            throw new DOMException("blocked");
          },
        },
        "en-US",
      ),
    ).not.toThrow();
  });
});
