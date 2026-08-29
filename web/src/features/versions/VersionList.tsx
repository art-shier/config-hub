import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { APIError } from "../../api/client";
import type {
  APIClientContract,
  DiffResult,
  Revision,
  RevisionChange,
  RevisionSummary,
} from "../../api/types";
import { ExactValue } from "../../components/ExactValue";
import { ModalDialog } from "../../components/ModalDialog";

interface RevisionListResponse {
  revisions: RevisionSummary[];
}

interface RevisionResponse {
  revision: Revision;
}

type LoadState = "idle" | "loading" | "ready" | "error";

export function VersionList({
  canWrite,
  client,
  environmentSlug,
  onRevisionChanged,
  projectSlug,
  refreshEpoch,
}: {
  client: APIClientContract;
  projectSlug: string;
  environmentSlug: string;
  canWrite: boolean;
  refreshEpoch: number;
  onRevisionChanged(revision: Revision): void;
}) {
  const [revisions, setRevisions] = useState<RevisionSummary[]>([]);
  const [loadState, setLoadState] = useState<LoadState>("idle");
  const [selected, setSelected] = useState<Revision | null>(null);
  const [diff, setDiff] = useState<DiffResult | null>(null);
  const [detailState, setDetailState] = useState<LoadState>("idle");
  const [selectedVersion, setSelectedVersion] = useState<number | null>(null);
  const [rollbackTarget, setRollbackTarget] = useState<RevisionSummary | null>(null);
  const [rollbackMessage, setRollbackMessage] = useState("");
  const [rollbackError, setRollbackError] = useState("");
  const [rollingBack, setRollingBack] = useState(false);
  const listGenerationRef = useRef(0);
  const detailGenerationRef = useRef(0);
  const rollbackGenerationRef = useRef(0);
  const rollingBackRef = useRef(false);

  const loadRevisions = useCallback(async () => {
    if (!environmentSlug) {
      setLoadState("idle");
      setRevisions([]);
      return;
    }
    const generation = ++listGenerationRef.current;
    setLoadState("loading");
    try {
      const response = await client.get<RevisionListResponse>(
        revisionsPath(projectSlug, environmentSlug),
      );
      if (listGenerationRef.current === generation) {
        setRevisions(response.revisions);
        setLoadState("ready");
      }
    } catch {
      if (listGenerationRef.current === generation) {
        setLoadState("error");
      }
    }
  }, [client, environmentSlug, projectSlug]);

  useEffect(() => {
    rollingBackRef.current = false;
    setRollingBack(false);
    setSelected(null);
    setDiff(null);
    setSelectedVersion(null);
    setRollbackTarget(null);
    setRollbackMessage("");
    setRollbackError("");
    void loadRevisions();
    return () => {
      listGenerationRef.current += 1;
      detailGenerationRef.current += 1;
      rollbackGenerationRef.current += 1;
    };
  }, [loadRevisions, refreshEpoch]);

  async function loadDetail(version: number) {
    const generation = ++detailGenerationRef.current;
    setSelectedVersion(version);
    setSelected(null);
    setDiff(null);
    setDetailState("loading");
    const basePath = `${revisionsPath(projectSlug, environmentSlug)}/${version}`;
    try {
      const [detailResponse, diffResponse] = await Promise.all([
        client.get<RevisionResponse>(basePath),
        client.get<DiffResult>(`${basePath}/diff`),
      ]);
      if (detailGenerationRef.current === generation) {
        setSelected(detailResponse.revision);
        setDiff(diffResponse);
        setDetailState("ready");
      }
    } catch {
      if (detailGenerationRef.current === generation) {
        setDetailState("error");
      }
    }
  }

  function openRollback(revision: RevisionSummary) {
    setRollbackTarget(revision);
    setRollbackMessage("");
    setRollbackError("");
  }

  function closeRollback() {
    if (!rollingBackRef.current) {
      setRollbackTarget(null);
      setRollbackError("");
    }
  }

  async function submitRollback(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (rollbackTarget === null || rollingBackRef.current) {
      return;
    }
    rollingBackRef.current = true;
    const generation = ++rollbackGenerationRef.current;
    const targetVersion = rollbackTarget.version;
    setRollingBack(true);
    setRollbackError("");
    try {
      const response = await client.post<RevisionResponse>(
        `${revisionsPath(projectSlug, environmentSlug)}/${targetVersion}/rollback`,
        { message: rollbackMessage },
      );
      if (rollbackGenerationRef.current === generation) {
        setRollbackTarget(null);
        rollingBackRef.current = false;
        setRollingBack(false);
        onRevisionChanged(response.revision);
      }
    } catch (error) {
      if (rollbackGenerationRef.current !== generation) {
        return;
      }
      if (error instanceof APIError && error.status === 422 && error.fields.message) {
        setRollbackError(error.fields.message);
      } else {
        setRollbackError("The system couldn’t create the rollback version. Keep this message and try again.");
      }
    } finally {
      if (rollbackGenerationRef.current === generation) {
        rollingBackRef.current = false;
        setRollingBack(false);
      }
    }
  }

  if (!environmentSlug) {
    return (
      <div className="empty-state compact-empty">
        <h2>Choose an environment</h2>
        <p>Select an environment above to review its version history.</p>
      </div>
    );
  }

  if (loadState === "idle" || loadState === "loading") {
    return <p className="loading-line" role="status">Loading version history…</p>;
  }

  if (loadState === "error") {
    return (
      <div className="inline-error-state">
        <h2>Version history unavailable</h2>
        <p>The version register couldn’t be loaded. Try again.</p>
        <button className="secondary-button" type="button" onClick={() => void loadRevisions()}>Retry</button>
      </div>
    );
  }

  return (
    <section className="version-workspace" aria-labelledby="versions-title">
      <header className="section-heading">
        <div>
          <p className="section-index">Revision register</p>
          <h2 id="versions-title">Versions</h2>
          <p>Open a version to compare its exact values with the current configuration.</p>
        </div>
      </header>

      {revisions.length === 0 ? (
        <div className="empty-state compact-empty">
          <h3>No versions yet</h3>
          <p>Save configuration changes to create the first version.</p>
        </div>
      ) : (
        <div className="version-layout">
          <div className="version-register" aria-label="Version history">
            {revisions.map((revision) => (
              <article
                key={revision.id}
                className={selectedVersion === revision.version ? "version-row selected-version" : "version-row"}
                aria-label={`Version ${revision.version}`}
              >
                <div className="version-row-main">
                  <p className="section-index">Version</p>
                  <h3 id={`version-${revision.version}-title`}>{revision.version}</h3>
                  <p>{revision.message || "No change message"}</p>
                </div>
                <dl className="version-meta">
                  <div><dt>Created by</dt><dd>{revision.created_by}</dd></div>
                  <div><dt>Created</dt><dd>{formatDateTime(revision.created_at)}</dd></div>
                </dl>
                <div className="version-actions">
                  <button className="text-button" type="button" onClick={() => void loadDetail(revision.version)}>
                    View version {revision.version}
                  </button>
                  {canWrite ? (
                    <button className="text-button" type="button" onClick={() => openRollback(revision)}>
                      Rollback to version {revision.version}
                    </button>
                  ) : null}
                </div>
              </article>
            ))}
          </div>
          <VersionDetail
            state={detailState}
            selected={selected}
            diff={diff}
            selectedVersion={selectedVersion}
            onRetry={(version) => void loadDetail(version)}
          />
        </div>
      )}

      {rollbackTarget !== null ? (
        <ModalDialog
          labelledBy="rollback-title"
          describedBy="rollback-description"
          closeDisabled={rollingBack}
          onRequestClose={closeRollback}
        >
          <header className="dialog-heading">
            <div>
              <p className="section-index">Revision register / Rollback</p>
              <h2 id="rollback-title">Rollback to version {rollbackTarget.version}?</h2>
            </div>
            <button className="text-button" type="button" disabled={rollingBack} onClick={closeRollback}>Cancel</button>
          </header>
          <p id="rollback-description">
            A rollback creates a new current version from version {rollbackTarget.version}; it does not remove later history.
          </p>
          <form className="resource-form" onSubmit={(event) => void submitRollback(event)}>
            <div className="form-field">
              <label htmlFor="rollback-message">Rollback message</label>
              <input
                id="rollback-message"
                value={rollbackMessage}
                disabled={rollingBack}
                aria-invalid={rollbackError ? "true" : undefined}
                aria-describedby={rollbackError ? "rollback-message-error" : undefined}
                onChange={(event) => {
                  setRollbackMessage(event.currentTarget.value);
                  setRollbackError("");
                }}
              />
              {rollbackError ? <p className="field-error" id="rollback-message-error" role="alert">{rollbackError}</p> : null}
            </div>
            <button className="primary-button" type="submit" disabled={rollingBack}>
              {rollingBack ? "Creating rollback…" : "Create rollback version"}
            </button>
          </form>
        </ModalDialog>
      ) : null}
    </section>
  );
}

function VersionDetail({
  diff,
  onRetry,
  selected,
  selectedVersion,
  state,
}: {
  diff: DiffResult | null;
  onRetry(version: number): void;
  selected: Revision | null;
  selectedVersion: number | null;
  state: LoadState;
}) {
  if (selectedVersion === null) {
    return (
      <aside className="version-detail empty-version-detail">
        <h3>Select a version</h3>
        <p>Choose a register row to inspect its details and current difference.</p>
      </aside>
    );
  }
  if (state === "loading") {
    return <p className="loading-line version-detail" role="status">Loading version {selectedVersion}…</p>;
  }
  if (state === "error" || selected === null || diff === null) {
    return (
      <aside className="version-detail inline-error-state">
        <h3>Version details unavailable</h3>
        <p>The selected version couldn’t be compared. Try again.</p>
        <button className="secondary-button" type="button" onClick={() => onRetry(selectedVersion)}>Retry version details</button>
      </aside>
    );
  }
  return (
    <aside className="version-detail" aria-labelledby="version-diff-title">
      <p className="section-index">Selected revision / {formatDateTime(selected.created_at)}</p>
      <h3 id="version-diff-title">Version {diff.before_revision} to current version {diff.after_revision}</h3>
      <p className="selected-revision-message">{selected.message || "No change message"}</p>
      {diff.changes.length === 0 ? <p>No configuration differences.</p> : (
        <div className="difference-list history-difference-list">
          {diff.changes.map((change) => <HistoryChange key={change.key} change={change} />)}
        </div>
      )}
    </aside>
  );
}

function HistoryChange({ change }: { change: RevisionChange }) {
  const beforePresent = change.kind !== "added";
  const afterPresent = change.kind !== "deleted";
  return (
    <article className="difference-row history-difference-row">
      <header>
        <span className={`change-kind change-${change.kind}`}>{change.kind}</span>
        <h4 className="code-label">{change.key}</h4>
      </header>
      <HistorySide side="before" label="Selected version" present={beforePresent} value={change.before} service={change.before_service} entryKey={change.key} />
      <HistorySide side="after" label="Current version" present={afterPresent} value={change.after} service={change.after_service} entryKey={change.key} />
    </article>
  );
}

function HistorySide({
  entryKey,
  label,
  present,
  service,
  side,
  value,
}: {
  entryKey: string;
  label: string;
  present: boolean;
  service: string;
  side: "before" | "after";
  value: string;
}) {
  return (
    <div className="difference-side">
      <p>{label}</p>
      {present ? (
        <ExactValue
          label={`${label} value for ${entryKey}`}
          testId={`diff-${side}-${entryKey}`}
          value={value}
        />
      ) : (
        <span className="absent-value" data-testid={`diff-${side}-${entryKey}`}>Absent</span>
      )}
      <span className="difference-service" data-testid={`diff-${side}-service-${entryKey}`}>
        Service: {present ? (service || <span className="empty-value">Empty string</span>) : <span className="absent-value">Absent</span>}
      </span>
    </div>
  );
}

function revisionsPath(projectSlug: string, environmentSlug: string): string {
  return `/projects/${encodeURIComponent(projectSlug)}/environments/${encodeURIComponent(environmentSlug)}/revisions`;
}

function formatDateTime(value: string): string {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "Time unavailable";
  try {
    return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
  } catch {
    return date.toISOString();
  }
}
