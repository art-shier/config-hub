import i18next, { type InitOptions } from "i18next";
import { initReactI18next } from "react-i18next";
import {
  DEFAULT_LOCALE,
  safeReadLocale,
  safeWriteLocale,
  resolvePreferredLocale,
  SUPPORTED_LOCALES,
  type SupportedLocale,
} from "./locales";
import { resources } from "./resources";

function browserLanguages(): readonly string[] {
  if (typeof navigator === "undefined") {
    return [];
  }
  if (navigator.languages.length > 0) {
    return navigator.languages;
  }
  return navigator.language ? [navigator.language] : [];
}

function browserStorage(): Pick<Storage, "getItem" | "setItem"> | null {
  if (typeof window === "undefined") {
    return null;
  }
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function synchronizeDocumentLanguage(locale: string): void {
  if (typeof document !== "undefined") {
    document.documentElement.lang = locale;
  }
}

const i18nOptions: InitOptions & { initImmediate: false } = {
  resources,
  lng: resolvePreferredLocale(
    safeReadLocale(browserStorage()),
    browserLanguages(),
  ),
  fallbackLng: DEFAULT_LOCALE,
  supportedLngs: SUPPORTED_LOCALES,
  nonExplicitSupportedLngs: false,
  defaultNS: "common",
  ns: [
    "common",
    "auth",
    "projects",
    "config",
    "versions",
    "members",
    "machineAccess",
    "system",
  ],
  interpolation: { escapeValue: false },
  // i18next 26 renamed the typed option to initAsync; retain the approved
  // initImmediate flag for compatible callers while making v26 synchronous.
  initImmediate: false,
  initAsync: false,
};

export const appI18n = i18next.createInstance();

appI18n.on("languageChanged", synchronizeDocumentLanguage);

void appI18n.use(initReactI18next).init(i18nOptions);

export async function changeLocale(locale: SupportedLocale): Promise<void> {
  await appI18n.changeLanguage(locale);
  synchronizeDocumentLanguage(locale);
  safeWriteLocale(browserStorage(), locale);
}
