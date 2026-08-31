import { useTranslation } from "react-i18next";
import { changeLocale } from "../i18n";
import type { SupportedLocale } from "../i18n/locales";

export function LanguageSwitcher({ className = "" }: { className?: string }) {
  const { i18n, t } = useTranslation("common");
  const locale: SupportedLocale =
    i18n.resolvedLanguage === "zh-CN" ? "zh-CN" : "en-US";

  return (
    <label className={`language-switcher ${className}`.trim()}>
      <span>{t("language.label")}</span>
      <select
        value={locale}
        onChange={(event) =>
          void changeLocale(event.currentTarget.value as SupportedLocale)
        }
      >
        <option value="en-US">English</option>
        <option value="zh-CN">简体中文</option>
      </select>
    </label>
  );
}
