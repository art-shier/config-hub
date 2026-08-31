import { describe, expect, it } from "vitest";
import { localizePresentFields } from "./apiErrors";

describe("localized API error fields", () => {
  it("maps known field presence without copying raw server values", () => {
    const mapped = localizePresentFields(
      { slug: "RAW SECRET", ignored: "RAW OTHER" },
      { slug: "项目标识不符合要求。" },
    );

    expect(mapped).toEqual({ slug: "项目标识不符合要求。" });
    expect(JSON.stringify(mapped)).not.toContain("RAW");
  });

  it("does not map inherited or unknown field keys", () => {
    const fields = Object.create({ slug: "inherited" }) as Record<string, string>;
    fields.unknown = "RAW UNKNOWN";

    expect(localizePresentFields(fields, { slug: "Invalid slug" })).toEqual({});
  });

  it("preserves known fields even when their server value is empty", () => {
    expect(localizePresentFields({ slug: "" }, { slug: "Invalid slug" })).toEqual({
      slug: "Invalid slug",
    });
  });
});
