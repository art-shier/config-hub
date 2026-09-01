import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
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
import { formatDateTime } from "../../i18n/format";
import type { SupportedLocale } from "../../i18n/locales";

interface RevisionListResponse {
  revisions: RevisionSummary[];
}

interface RevisionResponse {
  revision: Revision;
}

type LoadState = "idle" | "loading" | "ready" | "error";
type RollbackError = "validation" | "failure" | null;

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
  const { i18n, t } = useTranslation(["versions", "common"]);
  const locale: SupportedLocale = i18n.resolvedLanguage === "zh-CN" ? "zh-CN" : "en-US";
  const [revisions, setRevisions] = useState<RevisionSummary[]>([]);
  const [loadState, setLoadState] = useState<LoadState>("idle");
  const [selected, setSelected] = useState<Revision | null>(null);
  const [diff, setDiff] = useState<DiffResult | null>(null);
  const [detailState, setDetailState] = useState<LoadState>("idle");
  const [selectedVersion, setSelectedVersion] = useState<number | null>(null);
  const [rollbackTarget, setRollbackTarget] = useState<RevisionSummary | null>(null);
  const [rollbackMessage, setRollbackMessage] = useState("");
  const [rollbackError, setRollbackError] = useState<RollbackError>(null);
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
    setRollbackError(null);
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
    setRollbackError(null);
  }

  function closeRollback() {
    if (!rollingBackRef.current) {
      setRollbackTarget(null);
      setRollbackError(null);
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
    setRollbackError(null);
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
      if (error instanceof APIError && error.status === 422 && Object.hasOwn(error.fields, "message")) {
        setRollbackError("validation");
      } else {
        setRollbackError("failure");
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
          <h2>{t("states.chooseEnvironment")}</h2>
          <p>{t("states.chooseEnvironmentDescription")}</p>
      </div>
    );
  }

  if (loadState === "idle" || loadState === "loading") {
    return <p className="loading-line" role="status">{t("states.loading")}</p>;
  }

  if (loadState === "error") {
    return (
      <div className="inline-error-state">
        <h2>{t("states.unavailable")}</h2>
        <p>{t("states.unavailableDescription")}</p>
        <button className="secondary-button" type="button" onClick={() => void loadRevisions()}>{t("common:actions.retry")}</button>
      </div>
    );
  }

  return (
    <section className="version-workspace" aria-labelledby="versions-title">
      <header className="section-heading">
        <div>
          <p className="section-index">{t("register.index")}</p>
          <h2 id="versions-title">{t("register.title")}</h2>
          <p>{t("register.summary")}</p>
        </div>
      </header>

      {revisions.length === 0 ? (
        <div className="empty-state compact-empty">
          <h3>{t("register.emptyTitle")}</h3>
          <p>{t("register.emptyDescription")}</p>
        </div>
      ) : (
        <div className="version-layout">
          <div className="version-register" aria-label={t("register.accessibleName")}>
            {revisions.map((revision) => (
              <article
                key={revision.id}
                className={selectedVersion === revision.version ? "version-row selected-version" : "version-row"}
                aria-label={t("register.rowAccessibleName", { version: revision.version })}
              >
                <div className="version-row-main">
                  <p className="section-index">{t("register.version")}</p>
                  <h3 id={`version-${revision.version}-title`}>{revision.version}</h3>
                  <p>{revision.message || t("register.noMessage")}</p>
                </div>
                <dl className="version-meta">
                  <div>
                    <dt>{t("register.createdBy")}</dt>
                    <dd>{revision.created_by_type === "machine"
                      ? t("register.machineCreatedBy", { id: revision.created_by })
                      : revision.created_by}</dd>
                  </div>
                  <div><dt>{t("register.created")}</dt><dd>{formatDateTime(revision.created_at, locale, t("states.timeUnavailable"))}</dd></div>
                </dl>
                <div className="version-actions">
                  <button className="text-button" type="button" onClick={() => void loadDetail(revision.version)}>
                    {t("register.view", { version: revision.version })}
                  </button>
                  {canWrite ? (
                    <button className="text-button" type="button" onClick={() => openRollback(revision)}>
                      {t("register.rollback", { version: revision.version })}
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
            locale={locale}
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
              <p className="section-index">{t("rollback.index")}</p>
              <h2 id="rollback-title">{t("rollback.title", { version: rollbackTarget.version })}</h2>
            </div>
            <button className="text-button" type="button" disabled={rollingBack} onClick={closeRollback}>{t("common:actions.cancel")}</button>
          </header>
          <p id="rollback-description">
            {t("rollback.description", { version: rollbackTarget.version })}
          </p>
          <form className="resource-form" noValidate onSubmit={(event) => void submitRollback(event)}>
            <div className="form-field">
              <label htmlFor="rollback-message">{t("rollback.message")}</label>
              <input
                id="rollback-message"
                value={rollbackMessage}
                disabled={rollingBack}
                aria-invalid={rollbackError !== null ? "true" : undefined}
                aria-describedby={rollbackError !== null ? "rollback-message-error" : undefined}
                onChange={(event) => {
                  setRollbackMessage(event.currentTarget.value);
                  setRollbackError(null);
                }}
              />
              {rollbackError !== null ? (
                <p className="field-error" id="rollback-message-error" role="alert">
                  {rollbackError === "validation"
                    ? t("rollback.validation.message")
                    : t("rollback.failure")}
                </p>
              ) : null}
            </div>
            <button className="primary-button" type="submit" disabled={rollingBack}>
              {rollingBack ? t("rollback.pending") : t("rollback.action")}
            </button>
          </form>
        </ModalDialog>
      ) : null}
    </section>
  );
}

function VersionDetail({
  diff,
  locale,
  onRetry,
  selected,
  selectedVersion,
  state,
}: {
  diff: DiffResult | null;
  locale: SupportedLocale;
  onRetry(version: number): void;
  selected: Revision | null;
  selectedVersion: number | null;
  state: LoadState;
}) {
  const { t } = useTranslation(["versions", "common"]);
  if (selectedVersion === null) {
    return (
      <aside className="version-detail empty-version-detail">
        <h3>{t("detail.selectTitle")}</h3>
        <p>{t("detail.selectDescription")}</p>
      </aside>
    );
  }
  if (state === "loading") {
    return <p className="loading-line version-detail" role="status">{t("detail.loading", { version: selectedVersion })}</p>;
  }
  if (state === "error" || selected === null || diff === null) {
    return (
      <aside className="version-detail inline-error-state">
        <h3>{t("detail.unavailable")}</h3>
        <p>{t("detail.unavailableDescription")}</p>
        <button className="secondary-button" type="button" onClick={() => onRetry(selectedVersion)}>{t("detail.retry")}</button>
      </aside>
    );
  }
  return (
    <aside className="version-detail" aria-labelledby="version-diff-title">
      <p className="section-index">{t("detail.index", { createdAt: formatDateTime(selected.created_at, locale, t("states.timeUnavailable")) })}</p>
      <h3 id="version-diff-title">{t("detail.title", { before: diff.before_revision, after: diff.after_revision })}</h3>
      <p className="selected-revision-message">{selected.message || t("register.noMessage")}</p>
      {diff.changes.length === 0 ? <p>{t("detail.noDifferences")}</p> : (
        <div className="difference-list history-difference-list">
          {diff.changes.map((change) => <HistoryChange key={change.key} change={change} />)}
        </div>
      )}
    </aside>
  );
}

function HistoryChange({ change }: { change: RevisionChange }) {
  const { t } = useTranslation("versions");
  const beforePresent = change.kind !== "added";
  const afterPresent = change.kind !== "deleted";
  return (
    <article className="difference-row history-difference-row">
      <header>
        <span className={`change-kind change-${change.kind}`}>{t(`diff.${change.kind}`)}</span>
        <h4 className="code-label">{change.key}</h4>
      </header>
      <HistorySide side="before" label={t("diff.selected")} valueLabel={t("diff.selectedValue", { key: change.key })} present={beforePresent} value={change.before} service={change.before_service} entryKey={change.key} />
      <HistorySide side="after" label={t("diff.current")} valueLabel={t("diff.currentValue", { key: change.key })} present={afterPresent} value={change.after} service={change.after_service} entryKey={change.key} />
    </article>
  );
}

function HistorySide({
  entryKey,
  label,
  valueLabel,
  present,
  service,
  side,
  value,
}: {
  entryKey: string;
  label: string;
  valueLabel: string;
  present: boolean;
  service: string;
  side: "before" | "after";
  value: string;
}) {
  const { t } = useTranslation("versions");
  return (
    <div className="difference-side">
      <p>{label}</p>
      {present ? (
        <ExactValue
          label={valueLabel}
          testId={`diff-${side}-${entryKey}`}
          value={value}
        />
      ) : (
        <span className="absent-value" data-testid={`diff-${side}-${entryKey}`}>{t("diff.absent")}</span>
      )}
      <span className="difference-service" data-testid={`diff-${side}-service-${entryKey}`}>
        {t("diff.service")} {present ? (service || <span className="empty-value">{t("common:exactValue.emptyString")}</span>) : <span className="absent-value">{t("diff.absent")}</span>}
      </span>
    </div>
  );
}

function revisionsPath(projectSlug: string, environmentSlug: string): string {
  return `/projects/${encodeURIComponent(projectSlug)}/environments/${encodeURIComponent(environmentSlug)}/revisions`;
}
