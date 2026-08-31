export function localizePresentFields<K extends string>(
  fields: Record<string, string>,
  messages: Record<K, string>,
): Partial<Record<K, string>> {
  return Object.fromEntries(
    Object.keys(messages)
      .filter((key) => Object.hasOwn(fields, key))
      .map((key) => [key, messages[key as K]]),
  ) as Partial<Record<K, string>>;
}
