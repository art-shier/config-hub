import { expect, it } from "vitest";

it("uses the JSDOM storage implementation", () => {
  const descriptor = Object.getOwnPropertyDescriptor(window, "localStorage");
  expect({
    constructor: window.localStorage?.constructor?.name,
    descriptorHasGetter: typeof descriptor?.get === "function",
    getItem: typeof window.localStorage?.getItem,
    removeItem: typeof window.localStorage?.removeItem,
  }).toEqual({
    constructor: "Storage",
    descriptorHasGetter: true,
    getItem: "function",
    removeItem: "function",
  });
});
