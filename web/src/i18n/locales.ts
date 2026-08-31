export const SUPPORTED_LOCALES = ["en-US", "zh-CN"] as const;

export type SupportedLocale = (typeof SUPPORTED_LOCALES)[number];

export const DEFAULT_LOCALE: SupportedLocale = "en-US";
export const LOCALE_STORAGE_KEY = "confighub.locale";

function normalizeLocale(value: string): SupportedLocale | null {
  const normalized = value.trim().toLowerCase();
  if (normalized === "zh" || normalized.startsWith("zh-")) {
    return "zh-CN";
  }
  if (normalized === "en" || normalized.startsWith("en-")) {
    return "en-US";
  }
  return null;
}

export function resolvePreferredLocale(
  stored: string | null,
  browserLanguages: readonly string[],
): SupportedLocale {
  const persisted =
    stored === null
      ? null
      : SUPPORTED_LOCALES.find((locale) => locale === stored);
  if (persisted) {
    return persisted;
  }

  for (const language of browserLanguages) {
    const locale = normalizeLocale(language);
    if (locale) {
      return locale;
    }
  }

  return DEFAULT_LOCALE;
}

export function safeReadLocale(
  storage: Pick<Storage, "getItem"> | null,
): SupportedLocale | null {
  if (storage === null) {
    return null;
  }

  try {
    const stored = storage.getItem(LOCALE_STORAGE_KEY);
    return SUPPORTED_LOCALES.find((locale) => locale === stored) ?? null;
  } catch {
    return null;
  }
}

export function safeWriteLocale(
  storage: Pick<Storage, "setItem"> | null,
  locale: SupportedLocale,
): void {
  try {
    storage?.setItem(LOCALE_STORAGE_KEY, locale);
  } catch {
    // Storage can be blocked by privacy settings without blocking locale changes.
  }
}
