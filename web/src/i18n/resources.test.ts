import { describe, expect, it } from "vitest";
import { resources } from "./resources";

function sortedLeafKeys(value: object, prefix = ""): string[] {
  return Object.entries(value)
    .flatMap(([key, nested]) => {
      const path = prefix ? `${prefix}.${key}` : key;
      return typeof nested === "object" && nested !== null
        ? sortedLeafKeys(nested, path)
        : [path];
    })
    .sort();
}

describe("translation resources", () => {
  it("keeps every namespace and nested key in parity", () => {
    const namespaces = Object.keys(resources["en-US"]) as Array<
      keyof (typeof resources)["en-US"]
    >;

    for (const namespace of namespaces) {
      const english = sortedLeafKeys(resources["en-US"][namespace]);
      const chinese = sortedLeafKeys(resources["zh-CN"][namespace]);
      expect(english.length, `${namespace} must not be empty`).toBeGreaterThan(0);
      expect(chinese.length, `${namespace} must not be empty`).toBeGreaterThan(0);
      expect(chinese).toEqual(english);
    }
  });
});
