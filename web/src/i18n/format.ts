import type { SupportedLocale } from "./locales";

function format(
  value: string,
  locale: SupportedLocale,
  unavailable: string,
  options: Intl.DateTimeFormatOptions,
): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.valueOf())) {
    return unavailable;
  }

  try {
    return new Intl.DateTimeFormat(locale, options).format(parsed);
  } catch {
    return unavailable;
  }
}

export const formatDate = (
  value: string,
  locale: SupportedLocale,
  unavailable: string,
): string => format(value, locale, unavailable, { dateStyle: "medium" });

export const formatDateTime = (
  value: string,
  locale: SupportedLocale,
  unavailable: string,
): string =>
  format(value, locale, unavailable, {
    dateStyle: "medium",
    timeStyle: "short",
  });
