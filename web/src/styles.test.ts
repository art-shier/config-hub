import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const styles = readFileSync("src/styles.css", "utf8");

describe("responsive page styles", () => {
  it("does not force narrow text-zoomed viewports wider than the page", () => {
    expect(styles).toContain(":root");
    expect(styles).not.toMatch(/\bhtml\s*\{[^}]*\bmin-width\s*:/u);
    expect(styles).not.toMatch(/\bbody\s*\{[^}]*\bmin-width\s*:/u);
  });

  it("wraps logout feedback within the session summary", () => {
    expect(styles).toMatch(
      /\.logout-error\s*\{[^}]*\boverflow-wrap\s*:\s*anywhere/u,
    );
  });
});
