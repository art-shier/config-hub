import { useEffect, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useBlocker } from "react-router-dom";
import { APIError } from "../../api/client";
import type { APIClientContract, ConfigEntry, Revision } from "../../api/types";
import { ModalDialog } from "../../components/ModalDialog";
import {
  applyTextareaValueEdit,
  toTextareaDisplayValue,
  type TextareaEditMetadata,
} from "./configValueEditing";

interface CurrentRevisionResponse {
  revision: Revision;
}

type ConflictState = "none" | "needs-refresh" | "refreshing" | "ready" | "refresh-error";
type FieldErrorKey =
  | "entryDialog.validation.duplicateKey"
  | "entryDialog.validation.invalidKey"
  | "entryDialog.validation.key"
  | "entryDialog.validation.message"
  | "entryDialog.validation.service"
  | "entryDialog.validation.value";
type FormErrorKey =
  | "entryDialog.conflict.changed"
  | "entryDialog.saveUnavailable"
  | "entryDialog.validation.entries"
  | "entryDialog.validation.review";

export function ConfigEntryDialog({
  client,
  entry,
  environmentSlug,
  onCancel,
  onSaved,
  projectSlug,
  revision,
}: {
  client: APIClientContract;
  entry: ConfigEntry | null;
  environmentSlug: string;
  onCancel(): void;
  onSaved(revision: Revision): void;
  projectSlug: string;
  revision: Revision;
}) {
  const { t } = useTranslation(["config", "common"]);
  const [key, setKey] = useState(entry?.key ?? "");
  const [value, setValue] = useState(entry?.value ?? "");
  const [service, setService] = useState(entry?.service ?? "");
  const [message, setMessage] = useState("");
  const [baseRevision, setBaseRevision] = useState(revision.version);
  const [baseEntries, setBaseEntries] = useState(revision.entries);
  const [keyError, setKeyError] = useState<FieldErrorKey | null>(null);
  const [valueError, setValueError] = useState<FieldErrorKey | null>(null);
  const [serviceError, setServiceError] = useState<FieldErrorKey | null>(null);
  const [messageError, setMessageError] = useState<FieldErrorKey | null>(null);
  const [formError, setFormError] = useState<FormErrorKey | null>(null);
  const [valueEditNotice, setValueEditNotice] = useState(false);
  const [deleteError, setDeleteError] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [confirmingDiscard, setConfirmingDiscard] = useState(false);
  const [conflictState, setConflictState] = useState<ConflictState>("none");
  const [latestRevision, setLatestRevision] = useState<Revision | null>(null);
  const submittingRef = useRef(false);
  const operationGenerationRef = useRef(0);
  const refreshGenerationRef = useRef(0);
  const keyRef = useRef<HTMLInputElement>(null);
  const valueRef = useRef<HTMLTextAreaElement>(null);
  const serviceRef = useRef<HTMLInputElement>(null);
  const messageRef = useRef<HTMLInputElement>(null);
  const deleteCancelRef = useRef<HTMLButtonElement>(null);
  const discardKeepRef = useRef<HTMLButtonElement>(null);
  const pendingValueEditRef = useRef<(
    Pick<TextareaEditMetadata, "inputType" | "selectionStart" | "selectionEnd"> & {
      rawValue: string;
    }
  ) | null>(null);
  const latestRawValueRef = useRef(value);
  latestRawValueRef.current = value;
  const dirty = key !== (entry?.key ?? "") ||
    value !== (entry?.value ?? "") ||
    service !== (entry?.service ?? "") ||
    message !== "";
  const navigationBlocker = useBlocker(dirty);

  useEffect(() => {
    if (!dirty) return;
    function preventUnload(event: BeforeUnloadEvent) {
      event.preventDefault();
      event.returnValue = "";
    }
    window.addEventListener("beforeunload", preventUnload);
    return () => window.removeEventListener("beforeunload", preventUnload);
  }, [dirty]);

  useEffect(() => {
    if (navigationBlocker.state === "blocked") {
      setConfirmingDiscard(true);
    }
  }, [navigationBlocker.state]);

  useEffect(() => {
    const textarea = valueRef.current;
    if (textarea === null) return;
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
  }, [confirmingDelete, confirmingDiscard]);

  useEffect(() => {
    return () => {
      operationGenerationRef.current += 1;
      refreshGenerationRef.current += 1;
    };
  }, [environmentSlug, projectSlug]);

  function keepEditing() {
    if (navigationBlocker.state === "blocked") {
      navigationBlocker.reset();
    }
    setConfirmingDiscard(false);
  }

  function discardChanges() {
    if (navigationBlocker.state === "blocked") {
      navigationBlocker.proceed();
      return;
    }
    onCancel();
  }

  function requestClose() {
    if (dirty) setConfirmingDiscard(true);
    else onCancel();
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (submittingRef.current) {
      return;
    }
    submittingRef.current = true;
    const normalizedKey = key.trim();
    const duplicateKey = baseEntries.some(
      (current) => current.key !== entry?.key && current.key === normalizedKey,
    );
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/u.test(normalizedKey) || duplicateKey) {
      setKeyError(duplicateKey ? "entryDialog.validation.duplicateKey" : "entryDialog.validation.invalidKey");
      setFormError("entryDialog.validation.review");
      submittingRef.current = false;
      keyRef.current?.focus();
      return;
    }
    setKeyError(null);
    setValueError(null);
    setServiceError(null);
    setMessageError(null);
    setFormError(null);
    setSubmitting(true);
    const nextEntry = { key: normalizedKey, value, service: service.trim() };
    const entries = entry === null
      ? [...baseEntries, nextEntry]
      : replaceEntry(baseEntries, entry.key, nextEntry);
    const generation = ++operationGenerationRef.current;
    try {
      const response = await publishEntries(entries);
      if (operationGenerationRef.current === generation) {
        onSaved(response.revision);
      }
    } catch (error) {
      if (operationGenerationRef.current !== generation) return;
      if (error instanceof APIError && (error.status === 409 || error.code === "revision_conflict")) {
        setConflictState("needs-refresh");
        setLatestRevision(null);
        setFormError("entryDialog.conflict.changed");
      } else if (error instanceof APIError && error.status === 422) {
        const previousIndex = entry === null
          ? -1
          : baseEntries.findIndex((current) => current.key === entry.key);
        const targetIndex = previousIndex >= 0 ? previousIndex : entries.length - 1;
        let hasUnmappedEntryError = false;
        const invalidFields = new Set<"key" | "value" | "service" | "message">();
        for (const field of Object.keys(error.fields)) {
          if (field === "message") {
            setMessageError("entryDialog.validation.message");
            invalidFields.add("message");
            continue;
          }
          if (field === "entries") {
            hasUnmappedEntryError = true;
            continue;
          }
          const match = /^entries\[(\d+)\]\.(key|value|service)$/u.exec(field);
          if (match === null || Number(match[1]) !== targetIndex) {
            hasUnmappedEntryError = true;
            continue;
          }
          if (match[2] === "key") {
            setKeyError("entryDialog.validation.key");
            invalidFields.add("key");
          }
          if (match[2] === "value") {
            setValueError("entryDialog.validation.value");
            invalidFields.add("value");
          }
          if (match[2] === "service") {
            setServiceError("entryDialog.validation.service");
            invalidFields.add("service");
          }
        }
        setFormError(hasUnmappedEntryError
          ? "entryDialog.validation.entries"
          : "entryDialog.validation.review");
        const firstInvalid = (["key", "value", "service", "message"] as const)
          .find((field) => invalidFields.has(field));
        requestAnimationFrame(() => {
          if (firstInvalid === "key") keyRef.current?.focus();
          if (firstInvalid === "value") valueRef.current?.focus();
          if (firstInvalid === "service") serviceRef.current?.focus();
          if (firstInvalid === "message") messageRef.current?.focus();
        });
      } else {
        setFormError("entryDialog.saveUnavailable");
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

  async function handleDelete() {
    if (entry === null || submittingRef.current) {
      return;
    }
    submittingRef.current = true;
    setDeleteError(false);
    setSubmitting(true);
    const generation = ++operationGenerationRef.current;
    try {
      const response = await publishEntries(
        baseEntries.filter((current) => current.key !== entry.key),
      );
      if (operationGenerationRef.current === generation) {
        onSaved(response.revision);
      }
    } catch {
      if (operationGenerationRef.current === generation) {
        setDeleteError(true);
      }
    } finally {
      if (operationGenerationRef.current === generation) {
        submittingRef.current = false;
        setSubmitting(false);
      }
    }
  }

  function publishEntries(entries: ConfigEntry[]) {
    return client.put<CurrentRevisionResponse>(
      configPath(projectSlug, environmentSlug),
      { base_revision: baseRevision, message, entries },
    );
  }

  const latestEntry = latestRevision?.entries.find(
    (current) => current.key === (entry?.key ?? key.trim()),
  );

  if (confirmingDiscard) {
    return (
      <ModalDialog
        labelledBy="discard-configuration-entry-title"
        describedBy="discard-configuration-entry-description"
        initialFocusRef={discardKeepRef}
        closeDisabled={submitting}
        onRequestClose={keepEditing}
      >
        <header className="dialog-heading">
          <div>
            <p className="section-index">{t("entryDialog.discardIndex")}</p>
            <h2 id="discard-configuration-entry-title">{t("entryDialog.discardTitle")}</h2>
          </div>
        </header>
        <p id="discard-configuration-entry-description">{t("entryDialog.discardDescription")}</p>
        <div className="dialog-actions">
          <button
            ref={discardKeepRef}
            className="secondary-button"
            type="button"
            disabled={submitting}
            onClick={keepEditing}
          >
            {t("entryDialog.keepEditing")}
          </button>
          <button className="danger-button" type="button" disabled={submitting} onClick={discardChanges}>
            {t("entryDialog.discard")}
          </button>
        </div>
      </ModalDialog>
    );
  }

  if (confirmingDelete && entry !== null) {
    return (
      <ModalDialog
        labelledBy="delete-configuration-entry-title"
        describedBy="delete-configuration-entry-description"
        initialFocusRef={deleteCancelRef}
        closeDisabled={submitting}
        onRequestClose={() => setConfirmingDelete(false)}
      >
        <header className="dialog-heading">
          <div>
            <p className="section-index">{t("entryDialog.deleteIndex")}</p>
            <h2 id="delete-configuration-entry-title">
              {t("entryDialog.deleteTitle", { key: entry.key })}
            </h2>
          </div>
        </header>
        <p id="delete-configuration-entry-description">
          {t("entryDialog.deleteDescription")}
        </p>
        {deleteError ? (
          <p className="form-message" role="alert">{t("entryDialog.deleteUnavailable")}</p>
        ) : null}
        <div className="dialog-actions">
          <button
            ref={deleteCancelRef}
            className="secondary-button"
            type="button"
            disabled={submitting}
            onClick={() => {
              setDeleteError(false);
              setConfirmingDelete(false);
            }}
          >
            {t("common:actions.cancel")}
          </button>
          <button
            className="danger-button"
            type="button"
            disabled={submitting}
            onClick={() => void handleDelete()}
          >
            {t(submitting ? "entryDialog.deleting" : "entryDialog.deleteConfirm", { key: entry.key })}
          </button>
        </div>
      </ModalDialog>
    );
  }

  return (
    <ModalDialog
      labelledBy="configuration-entry-dialog-title"
      initialFocusRef={keyRef}
      closeDisabled={submitting}
      onRequestClose={requestClose}
    >
      <header className="dialog-heading">
        <div>
          <p className="section-index">
            {t(entry === null ? "entryDialog.addIndex" : "entryDialog.editIndex")}
          </p>
          <h2 id="configuration-entry-dialog-title">
            {t(entry === null ? "entryDialog.addTitle" : "entryDialog.editTitle")}
          </h2>
        </div>
        <button className="text-button" type="button" disabled={submitting} onClick={requestClose}>
          {t("common:actions.cancel")}
        </button>
      </header>
      <form className="resource-form configuration-entry-form" noValidate onSubmit={(event) => void handleSubmit(event)}>
        <div className="form-field">
          <label htmlFor="configuration-entry-key">{t("entryDialog.key")}</label>
          <input
            ref={keyRef}
            id="configuration-entry-key"
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            value={key}
            disabled={submitting}
            aria-invalid={keyError ? "true" : undefined}
            aria-describedby={keyError ? "configuration-entry-key-error" : undefined}
            onChange={(event) => {
              setKey(event.currentTarget.value);
              setKeyError(null);
              setFormError(null);
            }}
          />
          {keyError ? (
            <p className="field-error" id="configuration-entry-key-error">{t(keyError)}</p>
          ) : null}
        </div>
        <div className="form-field">
          <label htmlFor="configuration-entry-value">{t("entryDialog.value")}</label>
          <textarea
            ref={valueRef}
            id="configuration-entry-value"
            className="resize-none"
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            value={toTextareaDisplayValue(value)}
            disabled={submitting}
            aria-invalid={valueError ? "true" : undefined}
            aria-describedby={[
              valueError ? "configuration-entry-value-error" : "",
              valueEditNotice ? "configuration-entry-value-edit-notice" : "",
            ].filter(Boolean).join(" ") || undefined}
            onBlur={() => {
              pendingValueEditRef.current = null;
            }}
            onChange={(event) => {
              const pending = pendingValueEditRef.current;
              pendingValueEditRef.current = null;
              const result = applyTextareaValueEdit(
                value,
                event.currentTarget.value,
                pending !== null && pending.rawValue === value
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
              setValue(result.value);
              setValueEditNotice(false);
              setValueError(null);
              setFormError(null);
            }}
          />
          {valueError ? <p className="field-error" id="configuration-entry-value-error">{t(valueError)}</p> : null}
          {valueEditNotice ? (
            <p className="field-error" id="configuration-entry-value-edit-notice" role="alert">
              {t("entryDialog.unsupportedValueEdit")}
            </p>
          ) : null}
        </div>
        <div className="form-field">
          <label htmlFor="configuration-entry-service">{t("entryDialog.service")}</label>
          <input
            ref={serviceRef}
            id="configuration-entry-service"
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            value={service}
            disabled={submitting}
            aria-invalid={serviceError ? "true" : undefined}
            aria-describedby={serviceError ? "configuration-entry-service-error" : undefined}
            onChange={(event) => {
              setService(event.currentTarget.value);
              setServiceError(null);
              setFormError(null);
            }}
          />
          {serviceError ? <p className="field-error" id="configuration-entry-service-error">{t(serviceError)}</p> : null}
        </div>
        <div className="form-field">
          <label htmlFor="configuration-entry-message">{t("entryDialog.changeMessage")}</label>
          <input
            ref={messageRef}
            id="configuration-entry-message"
            value={message}
            disabled={submitting}
            aria-invalid={messageError ? "true" : undefined}
            aria-describedby={messageError ? "configuration-entry-message-error" : undefined}
            onChange={(event) => {
              setMessage(event.currentTarget.value);
              setMessageError(null);
              setFormError(null);
            }}
          />
          {messageError ? <p className="field-error" id="configuration-entry-message-error">{t(messageError)}</p> : null}
        </div>
        <div className="form-message configuration-entry-message" aria-live="polite">
          {formError ? <p role="alert">{t(formError)}</p> : null}
          {conflictState === "refresh-error" ? <p>{t("entryDialog.conflict.latestUnavailable")}</p> : null}
        </div>
        {conflictState !== "none" && conflictState !== "ready" ? (
          <button
            className="secondary-button configuration-conflict-action"
            type="button"
            disabled={conflictState === "refreshing" || submitting}
            onClick={() => void refreshConflict()}
          >
            {t(conflictState === "refreshing" ? "entryDialog.conflict.refreshing" : "entryDialog.conflict.refresh")}
          </button>
        ) : null}
        {latestRevision !== null && conflictState === "ready" ? (
          <section className="configuration-entry-conflict" aria-labelledby="configuration-entry-conflict-title">
            <p className="section-index">{t("entryDialog.conflict.index", { version: latestRevision.version })}</p>
            <h3 id="configuration-entry-conflict-title">{t("entryDialog.conflict.title")}</h3>
            <div className="configuration-entry-comparison">
              <div>
                <strong>{t("entryDialog.conflict.latest")}</strong>
                {latestEntry ? (
                  <dl className="configuration-entry-snapshot">
                    <div><dt>{t("entryDialog.key")}</dt><dd><code>{latestEntry.key}</code></dd></div>
                    <div><dt>{t("entryDialog.value")}</dt><dd><pre>{latestEntry.value}</pre></dd></div>
                    <div><dt>{t("entryDialog.service")}</dt><dd><code>{latestEntry.service}</code></dd></div>
                  </dl>
                ) : <p>{t("entryDialog.conflict.absent")}</p>}
              </div>
              <div>
                <strong>{t("entryDialog.conflict.draft")}</strong>
                <dl className="configuration-entry-snapshot">
                  <div><dt>{t("entryDialog.key")}</dt><dd><code>{key}</code></dd></div>
                  <div><dt>{t("entryDialog.value")}</dt><dd><pre>{value}</pre></dd></div>
                  <div><dt>{t("entryDialog.service")}</dt><dd><code>{service}</code></dd></div>
                </dl>
              </div>
            </div>
            <button
              className="secondary-button"
              type="button"
              onClick={() => {
                setBaseRevision(latestRevision.version);
                setBaseEntries(latestRevision.entries);
                setLatestRevision(null);
                setConflictState("none");
                setFormError(null);
              }}
            >
              {t("entryDialog.conflict.useBase", { version: latestRevision.version })}
            </button>
          </section>
        ) : null}
        <div className="dialog-actions configuration-entry-actions">
          {entry !== null ? (
            <button
              className="danger-button configuration-entry-delete"
              type="button"
              disabled={submitting}
              onClick={() => {
                setDeleteError(false);
                setConfirmingDelete(true);
              }}
            >
              {t("entryDialog.delete")}
            </button>
          ) : null}
          <button className="secondary-button" type="button" disabled={submitting} onClick={requestClose}>
            {t("common:actions.cancel")}
          </button>
          <button className="primary-button" type="submit" disabled={submitting || conflictState !== "none"}>
            {t(submitting ? "entryDialog.saving" : "entryDialog.save")}
          </button>
        </div>
      </form>
    </ModalDialog>
  );
}

function configPath(projectSlug: string, environmentSlug: string): string {
  return `/projects/${encodeURIComponent(projectSlug)}/environments/${encodeURIComponent(environmentSlug)}/config`;
}

function replaceEntry(entries: ConfigEntry[], originalKey: string, replacement: ConfigEntry): ConfigEntry[] {
  const index = entries.findIndex((current) => current.key === originalKey);
  if (index < 0) return [...entries, replacement];
  return entries.map((current, currentIndex) => currentIndex === index ? replacement : current);
}
