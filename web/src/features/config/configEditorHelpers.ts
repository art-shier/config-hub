import type { ConfigEntry } from "../../api/types";

export interface DraftEntry extends ConfigEntry {
  id: string;
}

export interface EntryErrors {
  key?: string;
  value?: string;
  service?: string;
}

export interface Comparison {
  key: string;
  server?: ConfigEntry;
  local?: ConfigEntry;
}

let nextDraftIdentifier = 0;

export function toDraftEntry(entry: ConfigEntry): DraftEntry {
  nextDraftIdentifier += 1;
  return { ...entry, id: `local-${nextDraftIdentifier}` };
}

export function sameSnapshot(draft: DraftEntry[], entries: ConfigEntry[]): boolean {
  const normalizedDraft = draft
    .map((entry) => ({ key: entry.key.trim(), value: entry.value, service: entry.service.trim() }))
    .sort((left, right) => left.key < right.key ? -1 : left.key > right.key ? 1 : 0);
  const orderedEntries = [...entries]
    .sort((left, right) => left.key < right.key ? -1 : left.key > right.key ? 1 : 0);
  return normalizedDraft.length === orderedEntries.length && normalizedDraft.every((entry, index) => {
    const loaded = orderedEntries[index];
    return loaded !== undefined &&
      entry.key === loaded.key &&
      entry.value === loaded.value &&
      entry.service === loaded.service;
  });
}

export function validateEntries(draft: DraftEntry[]): Record<string, EntryErrors> {
  const errors: Record<string, EntryErrors> = {};
  const seen = new Set<string>();
  for (const entry of draft) {
    const key = entry.key.trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/u.test(key)) {
      errors[entry.id] = { key: "Use letters, numbers, and underscores; start with a letter or underscore." };
    }
    if (seen.has(key)) {
      errors[entry.id] = { ...errors[entry.id], key: "Each key must be unique." };
    }
    seen.add(key);
  }
  return errors;
}

export function mapServerValidation(
  fields: Record<string, string>,
  submittedIds: string[],
): { entriesError: string; entryErrors: Record<string, EntryErrors>; messageError: string } {
  let entriesError = "";
  const entryErrors: Record<string, EntryErrors> = {};
  let messageError = "";
  for (const [field, value] of Object.entries(fields)) {
    if (field === "entries") {
      entriesError = value;
      continue;
    }
    if (field === "message") {
      messageError = value;
      continue;
    }
    const match = /^entries\[(\d+)\]\.(key|value|service)$/u.exec(field);
    if (match) {
      const id = submittedIds[Number(match[1])];
      const entryField = match[2] as keyof EntryErrors;
      if (id) entryErrors[id] = { ...entryErrors[id], [entryField]: value };
    }
  }
  return { entriesError, entryErrors, messageError };
}

export function compareEntries(serverEntries: ConfigEntry[], localDraft: DraftEntry[]): Comparison[] {
  const server = new Map(serverEntries.map((entry) => [entry.key, entry]));
  const local = new Map(localDraft.map((entry) => [entry.key.trim(), {
    key: entry.key.trim(), value: entry.value, service: entry.service.trim(),
  }]));
  return [...new Set([...server.keys(), ...local.keys()])].sort().flatMap((key) => {
    const serverEntry = server.get(key);
    const localEntry = local.get(key);
    if (serverEntry && localEntry && serverEntry.value === localEntry.value && serverEntry.service === localEntry.service) {
      return [];
    }
    return [{ key, server: serverEntry, local: localEntry }];
  });
}
