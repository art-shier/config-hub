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
    for (const namespace of Object.keys(resources["en-US"]) as Array<
      keyof (typeof resources)["en-US"]
    >) {
      expect(sortedLeafKeys(resources["zh-CN"][namespace])).toEqual(
        sortedLeafKeys(resources["en-US"][namespace]),
      );
    }
  });
});
