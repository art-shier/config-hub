import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useBlocker } from "react-router-dom";
import { APIError } from "../../api/client";
import type { APIClientContract, ConfigEntry, Revision } from "../../api/types";
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

interface CurrentRevisionResponse {
  revision: Revision;
}

type ConflictState = "none" | "needs-refresh" | "refreshing" | "ready" | "refresh-error";

export function ConfigEditor({
  client,
  environmentSlug,
  onCancel,
  onSaved,
  projectSlug,
  revision,
}: {
  client: APIClientContract;
  projectSlug: string;
  environmentSlug: string;
  revision: Revision;
  onCancel(): void;
  onSaved(revision: Revision): void;
}) {
  const [draft, setDraft] = useState<DraftEntry[]>(() =>
    revision.entries.map(toDraftEntry),
  );
  const [message, setMessage] = useState("");
  const [baseRevision, setBaseRevision] = useState(revision.version);
  const [entryErrors, setEntryErrors] = useState<Record<string, EntryErrors>>({});
  const [entriesError, setEntriesError] = useState("");
  const [messageError, setMessageError] = useState("");
  const [formError, setFormError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [confirmCancel, setConfirmCancel] = useState(false);
  const [conflictState, setConflictState] = useState<ConflictState>("none");
  const [latestRevision, setLatestRevision] = useState<Revision | null>(null);
  const submittingRef = useRef(false);
  const operationGenerationRef = useRef(0);
  const refreshGenerationRef = useRef(0);

  const dirty = useMemo(
    () => message !== "" || !sameSnapshot(draft, revision.entries),
    [draft, message, revision.entries],
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
    return () => {
      operationGenerationRef.current += 1;
      refreshGenerationRef.current += 1;
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
    setFormError("");
  }

  function addEntry() {
    setDraft((current) => [...current, toDraftEntry({ key: "", value: "", service: "" })]);
    setEntriesError("");
  }

  function deleteEntry(id: string) {
    setDraft((current) => current.filter((entry) => entry.id !== id));
    setEntryErrors((current) => {
      const next = { ...current };
      delete next[id];
      return next;
    });
    setEntriesError("");
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
    const localErrors = validateEntries(draft);
    setEntryErrors(localErrors);
    setEntriesError("");
    setMessageError("");
    setFormError("");
    if (Object.keys(localErrors).length > 0) {
      setFormError("Review the marked fields and try again.");
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
        const mapped = mapServerValidation(error.fields, submittedIds);
        setEntryErrors(mapped.entryErrors);
        setEntriesError(mapped.entriesError);
        setMessageError(mapped.messageError);
        setFormError("Review the marked fields and try again.");
      } else if (
        error instanceof APIError &&
        (error.status === 409 || error.code === "revision_conflict")
      ) {
        setConflictState("needs-refresh");
        setLatestRevision(null);
        setFormError("Configuration changed since you loaded it");
      } else {
        setFormError("The configuration couldn’t be saved. Your draft is still here; try again.");
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
    setFormError("Configuration changed since you loaded it");
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
    <section className="configuration-editor" aria-labelledby="configuration-editor-title">
      <header className="section-heading configuration-heading">
        <div>
          <p className="section-index">Draft register / Base version {baseRevision}</p>
          <h2 id="configuration-editor-title">Edit configuration</h2>
          <p>Keys and services are trimmed when saved. Values remain exact.</p>
        </div>
        <button
          className="text-button"
          type="button"
          disabled={submitting}
          onClick={requestCancel}
        >
          Cancel editing
        </button>
      </header>

      <form className="configuration-editor-form" onSubmit={(event) => void handleSubmit(event)}>
        <div
          className="configuration-draft-list"
          role="group"
          aria-label="Configuration entries"
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
              onDelete={() => deleteEntry(entry.id)}
            />
          ))}
        </div>
        <button className="secondary-button add-entry-button" type="button" disabled={submitting} onClick={addEntry}>
          Add entry
        </button>
        <div className="form-field configuration-message-field">
          <label htmlFor="configuration-message">Change message</label>
          <input
            id="configuration-message"
            value={message}
            disabled={submitting}
            aria-invalid={messageError ? "true" : undefined}
            aria-describedby={messageError ? "configuration-message-error" : undefined}
            onChange={(event) => {
              setMessage(event.currentTarget.value);
              setMessageError("");
            }}
          />
          {messageError ? <p className="field-error" id="configuration-message-error">{messageError}</p> : null}
        </div>

        <div className="form-message configuration-save-message" aria-live="polite">
          {formError ? <p role="alert">{formError}</p> : null}
          {conflictState === "refresh-error" ? (
            <p>The latest configuration couldn’t be loaded. Your draft is unchanged.</p>
          ) : null}
        </div>
        {conflictState !== "none" ? (
          <button
            className="secondary-button"
            type="button"
            disabled={conflictState === "refreshing" || submitting}
            onClick={() => void refreshConflict()}
          >
            {conflictState === "refreshing" ? "Refreshing…" : "Refresh and compare"}
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
              setConflictState("none");
              setFormError("");
            }}
          >
            Use version {latestRevision.version} as new base
          </button>
        ) : null}

        <button
          className="primary-button configuration-save-button"
          type="submit"
          disabled={submitting || !dirty || conflictState !== "none"}
        >
          {submitting ? "Saving…" : "Save changes"}
        </button>
      </form>

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
              <p className="section-index">Unsaved configuration</p>
              <h2 id="discard-configuration-title">Leave without saving?</h2>
            </div>
          </header>
          <p id="discard-configuration-description">Your configuration draft and change message will be discarded.</p>
          <div className="dialog-actions">
            <button
              className="secondary-button"
              type="button"
              disabled={submitting}
              onClick={() => {
                if (navigationBlocked) blocker.reset();
                setConfirmCancel(false);
              }}
            >Stay</button>
            <button
              className="primary-button"
              type="button"
              disabled={submitting}
              onClick={() => {
                if (navigationBlocked) blocker.proceed();
                else onCancel();
              }}
            >Discard and leave</button>
          </div>
        </ModalDialog>
      ) : null}
    </section>
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
  const label = entry.key.trim() || "new entry";
  const errorId = (field: keyof ConfigEntry) => `draft-${entry.id}-${field}-error`;
  return (
    <fieldset className="configuration-draft-row">
      <legend>Entry {index + 1}</legend>
      <div className="form-field draft-key-field">
        <label htmlFor={`draft-${entry.id}-key`}>Key for {label}</label>
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
        <label htmlFor={`draft-${entry.id}-value`}>Value for {label}</label>
        <textarea
          id={`draft-${entry.id}-value`}
          value={entry.value}
          disabled={disabled}
          aria-invalid={errors.value ? "true" : undefined}
          aria-describedby={errors.value ? errorId("value") : undefined}
          onChange={(event) => onChange("value", event.currentTarget.value)}
        />
        {errors.value ? <p className="field-error" id={errorId("value")}>{errors.value}</p> : null}
      </div>
      <div className="form-field draft-service-field">
        <label htmlFor={`draft-${entry.id}-service`}>Service for {label}</label>
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
        Delete {label}
      </button>
    </fieldset>
  );
}

function ConflictComparison({ comparisons, revision }: { comparisons: Comparison[]; revision: Revision }) {
  return (
    <section className="conflict-comparison" aria-labelledby="conflict-comparison-title">
      <p className="section-index">Server version {revision.version} / Local draft</p>
      <h3 id="conflict-comparison-title">Latest server compared with your draft</h3>
      {comparisons.length === 0 ? <p>The snapshots contain the same entries.</p> : (
        <div className="difference-list">
          {comparisons.map((comparison) => (
            <article className="difference-row" key={comparison.key}>
              <h4 className="code-label">{comparison.key}</h4>
              <DifferenceSide label="Latest server" entry={comparison.server} testId={`conflict-server-${comparison.key}`} />
              <DifferenceSide label="Your draft" entry={comparison.local} testId={`conflict-local-${comparison.key}`} />
            </article>
          ))}
        </div>
      )}
    </section>
  );
}

function DifferenceSide({ entry, label, testId }: { entry?: ConfigEntry; label: string; testId: string }) {
  return (
    <div className="difference-side">
      <p>{label}</p>
      {entry ? (
        <>
          <span className="configuration-value" data-testid={testId}>{entry.value || <span className="empty-value">Empty string</span>}</span>
          <span className="difference-service">Service: {entry.service || <span className="empty-value">Empty string</span>}</span>
        </>
      ) : <span className="absent-value">Absent</span>}
    </div>
  );
}

function configPath(projectSlug: string, environmentSlug: string): string {
  return `/projects/${encodeURIComponent(projectSlug)}/environments/${encodeURIComponent(environmentSlug)}/config`;
}
