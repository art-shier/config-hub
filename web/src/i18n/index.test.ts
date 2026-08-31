import { describe, expect, it } from "vitest";
import { appI18n, changeLocale } from "./index";

describe("application i18n", () => {
  it("updates the document language and persists zh-CN after an explicit change", async () => {
    await changeLocale("zh-CN");

    expect(appI18n.language).toBe("zh-CN");
    expect(document.documentElement.lang).toBe("zh-CN");
    expect(window.localStorage.getItem("confighub.locale")).toBe("zh-CN");
  });

  it("updates the document language and persists en-US after an explicit change", async () => {
    await changeLocale("en-US");

    expect(appI18n.language).toBe("en-US");
    expect(document.documentElement.lang).toBe("en-US");
    expect(window.localStorage.getItem("confighub.locale")).toBe("en-US");
  });
});
