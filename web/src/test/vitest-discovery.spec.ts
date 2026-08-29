import { describe, expect, it } from "vitest";

describe("Vitest discovery", () => {
  it("includes spec files under src without collecting Playwright E2E", () => {
    expect(import.meta.url).toContain("/src/test/vitest-discovery.spec.ts");
  });
});
