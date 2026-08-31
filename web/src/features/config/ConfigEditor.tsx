import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useBlocker } from "react-router-dom";
import { APIError } from "../../api/client";
import type { APIClientContract, ConfigEntry, Revision } from "../../api/types";
import { ExactValue } from "../../components/ExactValue";
import { ModalDialog } from "../../components/ModalDialog";
import {
  compareEntries,
  mapServerValidation,
  sameSnapshot,
  toDraftEntry,
  validateEntries,
  type Comparison,
  type DraftEntry,
  type EntryErrors,
} from "./configEditorHelpers";
import {
  applyTextareaValueEdit,
  toTextareaDisplayValue,
  type TextareaEditMetadata,
} from "./configValueEditing";

interface CurrentRevisionResponse {
  revision: Revision;
}

type ConflictState = "none" | "needs-refresh" | "refreshing" | "ready" | "refresh-error";
type FormErrorKey =
  | "editor.validation.review"
  | "editor.conflict.changed"
  | "editor.saveUnavailable";
type ValidationSource =
  | { kind: "local" }
  | { kind: "server"; fields: Record<string, string>; submittedIds: string[] };

export function ConfigEditor({
  client,
  editingUnavailable = false,
  environmentSlug,
  onCancel,
  onSaved,
  projectSlug,
  revision,
}: {
  client: APIClientContract;
  editingUnavailable?: boolean;
  projectSlug: string;
  environmentSlug: string;
  revision: Revision;
  onCancel(): void;
  onSaved(revision: Revision): void;
}) {
  const { i18n, t } = useTranslation(["config", "common"]);
  const [draft, setDraft] = useState<DraftEntry[]>(() =>
    revision.entries.map(toDraftEntry),
  );
  const [message, setMessage] = useState("");
  const [baseRevision, setBaseRevision] = useState(revision.version);
  const [baselineEntries, setBaselineEntries] = useState(revision.entries);
  const [entryErrors, setEntryErrors] = useState<Record<string, EntryErrors>>({});
  const [entriesError, setEntriesError] = useState("");
  const [messageError, setMessageError] = useState("");
  const [formError, setFormError] = useState<FormErrorKey | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [confirmCancel, setConfirmCancel] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<DraftEntry | null>(null);
  const [conflictState, setConflictState] = useState<ConflictState>("none");
  const [latestRevision, setLatestRevision] = useState<Revision | null>(null);
  const submittingRef = useRef(false);
  const operationGenerationRef = useRef(0);
  const refreshGenerationRef = useRef(0);
  const deleteFocusFrameRef = useRef<number | null>(null);
  const editorHeadingRef = useRef<HTMLHeadingElement>(null);
  const addEntryRef = useRef<HTMLButtonElement>(null);
  const validationSourceRef = useRef<ValidationSource | null>(null);

  const dirty = useMemo(
    () => message !== "" || !sameSnapshot(draft, baselineEntries),
    [baselineEntries, draft, message],
  );
  const blocker = useBlocker(dirty);
  const navigationBlocked = blocker.state === "blocked";

  useEffect(() => {
    if (!dirty) {
      return;
    }
    function preventUnload(event: BeforeUnloadEvent) {
      event.preventDefault();
      event.returnValue = "";
    }
    window.addEventListener("beforeunload", preventUnload);
    return () => window.removeEventListener("beforeunload", preventUnload);
  }, [dirty]);

  useEffect(() => {
    const source = validationSourceRef.current;
    if (source === null) {
      return;
    }
    if (source.kind === "local") {
      setEntryErrors(validateEntries(draft, {
        invalidKey: t("editor.validation.invalidKey"),
        duplicateKey: t("editor.validation.duplicateKey"),
      }));
      return;
    }
    const mapped = mapServerValidation(source.fields, source.submittedIds, {
      entries: t("editor.validation.entries"),
      message: t("editor.validation.message"),
      key: t("editor.validation.key"),
      value: t("editor.validation.value"),
      service: t("editor.validation.service"),
    });
    setEntryErrors(mapped.entryErrors);
    setEntriesError(mapped.entriesError);
    setMessageError(mapped.messageError);
  }, [i18n.resolvedLanguage]);

  useEffect(() => {
    return () => {
      operationGenerationRef.current += 1;
      refreshGenerationRef.current += 1;
      if (deleteFocusFrameRef.current !== null) {
        cancelAnimationFrame(deleteFocusFrameRef.current);
      }
    };
  }, [environmentSlug, projectSlug]);

  function updateEntry(id: string, field: keyof ConfigEntry, value: string) {
    setDraft((current) =>
      current.map((entry) => entry.id === id ? { ...entry, [field]: value } : entry),
    );
    setEntryErrors((current) => {
      const next = { ...current, [id]: { ...current[id], [field]: undefined } };
      return next;
    });
    setEntriesError("");
    setFormError(null);
    const source = validationSourceRef.current;
    if (source?.kind === "server") {
      const submittedIndex = source.submittedIds.indexOf(id);
      if (submittedIndex >= 0) {
        const fields = { ...source.fields };
        delete fields[`entries[${submittedIndex}].${field}`];
        delete fields.entries;
        validationSourceRef.current = { ...source, fields };
      }
    }
  }

  function addEntry() {
    setDraft((current) => [...current, toDraftEntry({ key: "", value: "", service: "" })]);
    setEntriesError("");
    clearServerEntriesError();
  }

  function deleteEntry(id: string) {
    setDraft((current) => current.filter((entry) => entry.id !== id));
    setEntryErrors((current) => {
      const next = { ...current };
      delete next[id];
      return next;
    });
    setEntriesError("");
    clearServerEntriesError();
  }

  function clearServerEntriesError() {
    const source = validationSourceRef.current;
    if (source?.kind !== "server") {
      return;
    }
    const fields = { ...source.fields };
    delete fields.entries;
    validationSourceRef.current = { ...source, fields };
  }

  function confirmDeleteEntry() {
    if (pendingDelete === null) {
      return;
    }
    const index = draft.findIndex((entry) => entry.id === pendingDelete.id);
    const nextEntry = index >= 0 ? draft[index + 1] : undefined;
    deleteEntry(pendingDelete.id);
    setPendingDelete(null);
    if (deleteFocusFrameRef.current !== null) {
      cancelAnimationFrame(deleteFocusFrameRef.current);
    }
    deleteFocusFrameRef.current = requestAnimationFrame(() => {
      const nextControl = nextEntry === undefined
        ? addEntryRef.current ?? editorHeadingRef.current
        : document.getElementById(`draft-${nextEntry.id}-key`);
      nextControl?.focus();
      deleteFocusFrameRef.current = null;
    });
  }

  function requestCancel() {
    if (dirty) {
      setConfirmCancel(true);
    } else {
      onCancel();
    }
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (submittingRef.current || conflictState !== "none") {
      return;
    }
    const normalized = draft.map((entry) => ({
      key: entry.key.trim(),
      value: entry.value,
      service: entry.service.trim(),
    }));
    const localErrors = validateEntries(draft, {
      invalidKey: t("editor.validation.invalidKey"),
      duplicateKey: t("editor.validation.duplicateKey"),
    });
    validationSourceRef.current = Object.keys(localErrors).length > 0 ? { kind: "local" } : null;
    setEntryErrors(localErrors);
    setEntriesError("");
    setMessageError("");
    setFormError(null);
    if (Object.keys(localErrors).length > 0) {
      setFormError("editor.validation.review");
      return;
    }

    submittingRef.current = true;
    const generation = ++operationGenerationRef.current;
    const submittedIds = draft.map((entry) => entry.id);
    setSubmitting(true);
    try {
      const response = await client.put<CurrentRevisionResponse>(
        configPath(projectSlug, environmentSlug),
        { base_revision: baseRevision, message, entries: normalized },
      );
      if (operationGenerationRef.current === generation) {
        onSaved(response.revision);
      }
    } catch (error) {
      if (operationGenerationRef.current !== generation) {
        return;
      }
      if (error instanceof APIError && error.status === 422) {
        const safeFields = Object.fromEntries(Object.keys(error.fields).map((field) => [field, ""]));
        validationSourceRef.current = { kind: "server", fields: safeFields, submittedIds };
        const mapped = mapServerValidation(safeFields, submittedIds, {
          entries: t("editor.validation.entries"),
          message: t("editor.validation.message"),
          key: t("editor.validation.key"),
          value: t("editor.validation.value"),
          service: t("editor.validation.service"),
        });
        setEntryErrors(mapped.entryErrors);
        setEntriesError(mapped.entriesError);
        setMessageError(mapped.messageError);
        setFormError("editor.validation.review");
      } else if (
        error instanceof APIError &&
        (error.status === 409 || error.code === "revision_conflict")
      ) {
        setConflictState("needs-refresh");
        setLatestRevision(null);
        setFormError("editor.conflict.changed");
      } else {
        setFormError("editor.saveUnavailable");
      }
    } finally {
      if (operationGenerationRef.current === generation) {
        submittingRef.current = false;
        setSubmitting(false);
      }
    }
  }

  async function refreshConflict() {
    const generation = ++refreshGenerationRef.current;
    setConflictState("refreshing");
    setFormError("editor.conflict.changed");
    try {
      const response = await client.get<CurrentRevisionResponse>(
        configPath(projectSlug, environmentSlug),
      );
      if (refreshGenerationRef.current === generation) {
        setLatestRevision(response.revision);
        setConflictState("ready");
      }
    } catch {
      if (refreshGenerationRef.current === generation) {
        setConflictState("refresh-error");
      }
    }
  }

  const comparisons = useMemo(
    () => latestRevision === null ? [] : compareEntries(latestRevision.entries, draft),
    [draft, latestRevision],
  );
  const leaveDialogOpen = navigationBlocked || confirmCancel;

  return (
    <>
    <section
      className="configuration-editor"
      aria-labelledby="configuration-editor-title"
      hidden={editingUnavailable}
    >
      <header className="section-heading configuration-heading">
        <div>
          <p className="section-index">{t("editor.index", { version: baseRevision })}</p>
          <h2 ref={editorHeadingRef} id="configuration-editor-title" tabIndex={-1}>{t("editor.title")}</h2>
          <p>{t("editor.summary")}</p>
        </div>
        <button
          className="text-button"
          type="button"
          disabled={submitting}
          onClick={requestCancel}
        >
          {t("editor.cancel")}
        </button>
      </header>

      <form className="configuration-editor-form" noValidate onSubmit={(event) => void handleSubmit(event)}>
        <div
          className="configuration-draft-list"
          role="group"
          aria-label={t("editor.entries")}
          aria-describedby={entriesError ? "configuration-entries-error" : undefined}
        >
          {entriesError ? (
            <p className="field-error" id="configuration-entries-error">{entriesError}</p>
          ) : null}
          {draft.map((entry, index) => (
            <DraftRow
              key={entry.id}
              entry={entry}
              index={index}
              errors={entryErrors[entry.id]}
              disabled={submitting}
              onChange={(field, value) => updateEntry(entry.id, field, value)}
              onDelete={() => setPendingDelete(entry)}
            />
          ))}
        </div>
        <button ref={addEntryRef} className="secondary-button add-entry-button" type="button" disabled={submitting} onClick={addEntry}>
          {t("editor.addEntry")}
        </button>
        <div className="form-field configuration-message-field">
          <label htmlFor="configuration-message">{t("editor.changeMessage")}</label>
          <input
            id="configuration-message"
            value={message}
            disabled={submitting}
            aria-invalid={messageError ? "true" : undefined}
            aria-describedby={messageError ? "configuration-message-error" : undefined}
            onChange={(event) => {
              setMessage(event.currentTarget.value);
              setMessageError("");
              const source = validationSourceRef.current;
              if (source?.kind === "server") {
                const fields = { ...source.fields };
                delete fields.message;
                validationSourceRef.current = { ...source, fields };
              }
            }}
          />
          {messageError ? <p className="field-error" id="configuration-message-error">{messageError}</p> : null}
        </div>

        <div className="form-message configuration-save-message" aria-live="polite">
          {formError ? <p role="alert">{t(formError)}</p> : null}
          {conflictState === "refresh-error" ? (
            <p>{t("editor.conflict.latestUnavailable")}</p>
          ) : null}
        </div>
        {conflictState !== "none" ? (
          <button
            className="secondary-button"
            type="button"
            disabled={conflictState === "refreshing" || submitting}
            onClick={() => void refreshConflict()}
          >
            {t(conflictState === "refreshing" ? "editor.conflict.refreshing" : "editor.conflict.refresh")}
          </button>
        ) : null}

        {latestRevision !== null && conflictState === "ready" ? (
          <ConflictComparison revision={latestRevision} comparisons={comparisons} />
        ) : null}
        {latestRevision !== null && conflictState === "ready" ? (
          <button
            className="secondary-button"
            type="button"
            onClick={() => {
              setBaseRevision(latestRevision.version);
              setBaselineEntries(latestRevision.entries);
              setConflictState("none");
              setFormError(null);
            }}
          >
            {t("editor.conflict.useBase", { version: latestRevision.version })}
          </button>
        ) : null}

        <button
          className="primary-button configuration-save-button"
          type="submit"
          disabled={submitting || !dirty || conflictState !== "none"}
        >
          {t(submitting ? "editor.saving" : "editor.save")}
        </button>
      </form>

      {pendingDelete !== null ? (
        <ModalDialog
          labelledBy="delete-configuration-entry-title"
          describedBy="delete-configuration-entry-description"
          onRequestClose={() => setPendingDelete(null)}
        >
          <header className="dialog-heading">
            <div>
              <p className="section-index">{t("editor.remove.eyebrow")}</p>
              <h2 id="delete-configuration-entry-title">
                {t("editor.remove.title", { key: pendingDelete.key.trim() || t("editor.newEntry") })}
              </h2>
            </div>
          </header>
          <p id="delete-configuration-entry-description">
            {t("editor.remove.description")}
          </p>
          <div className="dialog-actions">
            <button className="secondary-button" type="button" onClick={() => setPendingDelete(null)}>
              {t("common:actions.cancel")}
            </button>
            <button className="primary-button" type="button" onClick={confirmDeleteEntry}>
              {t("editor.remove.action", { key: pendingDelete.key.trim() || t("editor.newEntry") })}
            </button>
          </div>
        </ModalDialog>
      ) : null}
    </section>

      {leaveDialogOpen ? (
        <ModalDialog
          labelledBy="discard-configuration-title"
          describedBy="discard-configuration-description"
          closeDisabled={submitting}
          onRequestClose={() => {
            if (navigationBlocked) blocker.reset();
            setConfirmCancel(false);
          }}
        >
          <header className="dialog-heading">
            <div>
              <p className="section-index">{t("editor.leave.eyebrow")}</p>
              <h2 id="discard-configuration-title">{t("editor.leave.title")}</h2>
            </div>
          </header>
          <p id="discard-configuration-description">{t("editor.leave.description")}</p>
          <div className="dialog-actions">
            <button
              className="secondary-button"
              type="button"
              disabled={submitting}
              onClick={() => {
                if (navigationBlocked) blocker.reset();
                setConfirmCancel(false);
              }}
            >{t("editor.leave.stay")}</button>
            <button
              className="primary-button"
              type="button"
              disabled={submitting}
              onClick={() => {
                if (navigationBlocked) blocker.proceed();
                else onCancel();
              }}
            >{t("editor.leave.discard")}</button>
          </div>
        </ModalDialog>
      ) : null}
    </>
  );
}

function DraftRow({
  disabled,
  entry,
  errors = {},
  index,
  onChange,
  onDelete,
}: {
  disabled: boolean;
  entry: DraftEntry;
  errors?: EntryErrors;
  index: number;
  onChange(field: keyof ConfigEntry, value: string): void;
  onDelete(): void;
}) {
  const { t } = useTranslation("config");
  const label = entry.key.trim() || t("editor.newEntry");
  const errorId = (field: keyof ConfigEntry) => `draft-${entry.id}-${field}-error`;
  const valueEditNoticeId = `draft-${entry.id}-value-edit-notice`;
  const [valueEditNotice, setValueEditNotice] = useState(false);
  const pendingValueEditRef = useRef<(
    Pick<TextareaEditMetadata, "inputType" | "selectionStart" | "selectionEnd"> & {
      rawValue: string;
    }
  ) | null>(null);
  const valueTextareaRef = useRef<HTMLTextAreaElement>(null);
  const latestRawValueRef = useRef(entry.value);
  latestRawValueRef.current = entry.value;
  useEffect(() => {
    const textarea = valueTextareaRef.current;
    if (textarea === null) {
      return;
    }
    const captureValueEdit = (event: InputEvent) => {
      pendingValueEditRef.current = {
        inputType: event.inputType,
        selectionStart: textarea.selectionStart,
        selectionEnd: textarea.selectionEnd,
        rawValue: latestRawValueRef.current,
      };
    };
    textarea.addEventListener("beforeinput", captureValueEdit);
    return () => textarea.removeEventListener("beforeinput", captureValueEdit);
  }, []);
  const valueDescription = [
    errors.value ? errorId("value") : "",
    valueEditNotice ? valueEditNoticeId : "",
  ].filter(Boolean).join(" ") || undefined;
  return (
    <fieldset className="configuration-draft-row">
      <legend>{t("editor.entry", { index: index + 1 })}</legend>
      <div className="form-field draft-key-field">
        <label htmlFor={`draft-${entry.id}-key`}>{t("editor.key", { key: label })}</label>
        <input
          id={`draft-${entry.id}-key`}
          autoCapitalize="none"
          autoComplete="off"
          spellCheck={false}
          value={entry.key}
          disabled={disabled}
          aria-invalid={errors.key ? "true" : undefined}
          aria-describedby={errors.key ? errorId("key") : undefined}
          onChange={(event) => onChange("key", event.currentTarget.value)}
        />
        {errors.key ? <p className="field-error" id={errorId("key")}>{errors.key}</p> : null}
      </div>
      <div className="form-field draft-value-field">
        <label htmlFor={`draft-${entry.id}-value`}>{t("editor.value", { key: label })}</label>
        <textarea
          ref={valueTextareaRef}
          id={`draft-${entry.id}-value`}
          className="resize-none"
          style={{ resize: "none" }}
          value={toTextareaDisplayValue(entry.value)}
          disabled={disabled}
          aria-invalid={errors.value ? "true" : undefined}
          aria-describedby={valueDescription}
          onBlur={() => {
            pendingValueEditRef.current = null;
          }}
          onChange={(event) => {
            const pending = pendingValueEditRef.current;
            pendingValueEditRef.current = null;
            const result = applyTextareaValueEdit(
              entry.value,
              event.currentTarget.value,
              pending !== null && pending.rawValue === entry.value
                ? {
                    inputType: pending.inputType,
                    selectionStart: pending.selectionStart,
                    selectionEnd: pending.selectionEnd,
                    nextSelectionStart: event.currentTarget.selectionStart,
                    nextSelectionEnd: event.currentTarget.selectionEnd,
                  }
                : null,
            );
            if (result.kind === "unsupported") {
              setValueEditNotice(true);
              return;
            }
            setValueEditNotice(false);
            onChange("value", result.value);
          }}
        />
        {errors.value ? <p className="field-error" id={errorId("value")}>{errors.value}</p> : null}
        {valueEditNotice ? (
          <p className="field-error" id={valueEditNoticeId} role="alert">
            {t("editor.unsupportedValueEdit")}
          </p>
        ) : null}
      </div>
      <div className="form-field draft-service-field">
        <label htmlFor={`draft-${entry.id}-service`}>{t("editor.service", { key: label })}</label>
        <input
          id={`draft-${entry.id}-service`}
          value={entry.service}
          disabled={disabled}
          aria-invalid={errors.service ? "true" : undefined}
          aria-describedby={errors.service ? errorId("service") : undefined}
          onChange={(event) => onChange("service", event.currentTarget.value)}
        />
        {errors.service ? <p className="field-error" id={errorId("service")}>{errors.service}</p> : null}
      </div>
      <button className="text-button draft-delete-button" type="button" disabled={disabled} onClick={onDelete}>
        {t("editor.delete", { key: label })}
      </button>
    </fieldset>
  );
}

function ConflictComparison({ comparisons, revision }: { comparisons: Comparison[]; revision: Revision }) {
  const { t } = useTranslation("config");
  return (
    <section className="conflict-comparison" aria-labelledby="conflict-comparison-title">
      <p className="section-index">{t("editor.conflict.index", { version: revision.version })}</p>
      <h3 id="conflict-comparison-title">{t("editor.conflict.title")}</h3>
      {comparisons.length === 0 ? <p>{t("editor.conflict.same")}</p> : (
        <div className="difference-list">
          {comparisons.map((comparison) => (
            <article className="difference-row" key={comparison.key}>
              <h4 className="code-label">{comparison.key}</h4>
              <DifferenceSide
                label={t("editor.conflict.latest")}
                valueLabel={t("editor.conflict.latestValue", { key: comparison.key })}
                entry={comparison.server}
                testId={`conflict-server-${comparison.key}`}
              />
              <DifferenceSide
                label={t("editor.conflict.draft")}
                valueLabel={t("editor.conflict.draftValue", { key: comparison.key })}
                entry={comparison.local}
                testId={`conflict-local-${comparison.key}`}
              />
            </article>
          ))}
        </div>
      )}
    </section>
  );
}

function DifferenceSide({
  entry,
  label,
  testId,
  valueLabel,
}: {
  entry?: ConfigEntry;
  label: string;
  testId: string;
  valueLabel: string;
}) {
  const { t } = useTranslation("config");
  return (
    <div className="difference-side">
      <p>{label}</p>
      {entry ? (
        <>
          <ExactValue label={valueLabel} testId={testId} value={entry.value} />
          <span className="difference-service">{t("editor.conflict.service")} {entry.service || <span className="empty-value">{t("editor.conflict.emptyString")}</span>}</span>
        </>
      ) : <span className="absent-value" data-testid={testId}>{t("editor.conflict.absent")}</span>}
    </div>
  );
}

function configPath(projectSlug: string, environmentSlug: string): string {
  return `/projects/${encodeURIComponent(projectSlug)}/environments/${encodeURIComponent(environmentSlug)}/config`;
}
