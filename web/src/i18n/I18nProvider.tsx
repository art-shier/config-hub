import { I18nextProvider } from "react-i18next";
import type { ReactNode } from "react";
import { appI18n } from "./index";

export function I18nProvider({ children }: { children: ReactNode }) {
  return <I18nextProvider i18n={appI18n}>{children}</I18nextProvider>;
}
