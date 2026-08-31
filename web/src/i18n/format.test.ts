import { afterEach, describe, expect, it, vi } from "vitest";
import { formatDate, formatDateTime } from "./format";

describe("localized date formatting", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("formats the same instant with the explicitly supplied locale", () => {
    const instant = "2026-08-31T04:05:00Z";

    const english = formatDateTime(instant, "en-US", "Unavailable");
    const chinese = formatDateTime(instant, "zh-CN", "不可用");

    expect(english).toContain("2026");
    expect(chinese).toContain("2026");
    expect(english).not.toBe("Unavailable");
    expect(chinese).not.toBe("不可用");
    expect(english).not.toBe(chinese);
  });

  it("uses the caller's localized fallback for invalid dates", () => {
    expect(formatDate("not-a-date", "zh-CN", "不可用")).toBe("不可用");
  });

  it("uses the caller's localized fallback when Intl formatting fails", () => {
    vi.stubGlobal(
      "Intl",
      {
        ...Intl,
        DateTimeFormat: class {
          constructor() {
            throw new RangeError("unsupported locale");
          }
        },
      },
    );

    expect(formatDateTime("2026-08-31T04:05:00Z", "en-US", "Unavailable")).toBe(
      "Unavailable",
    );
  });
});
