import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { I18nProvider } from "../i18n/I18nProvider";
import { LanguageSwitcher } from "./LanguageSwitcher";

describe("language switching", () => {
  // Break caught: removing the shared locale selector or bypassing explicit locale persistence.
  it("changes the document language and persists the explicit choice", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <LanguageSwitcher />
      </I18nProvider>,
    );

    await user.selectOptions(
      await screen.findByRole("combobox", { name: "Language" }),
      "zh-CN",
    );

    expect(document.documentElement.lang).toBe("zh-CN");
    expect(localStorage.getItem("confighub.locale")).toBe("zh-CN");
    expect(screen.getByRole("combobox", { name: "\u8bed\u8a00" })).toHaveValue("zh-CN");
  });
});
