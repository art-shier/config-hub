import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { APIClientContract, Revision } from "../../api/types";
import { OverflowText } from "../../components/OverflowText";
import { ConfigEntryDialog } from "./ConfigEntryDialog";

interface CurrentRevisionResponse {
  revision: Revision;
}

type LoadState = "idle" | "loading" | "ready" | "error";

export function ConfigTable({
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
  const { t } = useTranslation(["config", "common"]);
  const [revision, setRevision] = useState<Revision | null>(null);
  const [loadState, setLoadState] = useState<LoadState>("idle");
  const [creatingEntry, setCreatingEntry] = useState(false);
  const [editingEntryKey, setEditingEntryKey] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [savedVersion, setSavedVersion] = useState<number | null>(null);
  const generationRef = useRef(0);
  const skipOwnRefreshRef = useRef(false);
  const configurationHeadingRef = useRef<HTMLHeadingElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const focusTargetRef = useRef<"saved" | null>(null);

  const loadCurrent = useCallback(async () => {
    setSavedVersion(null);
    if (!environmentSlug) {
      setRevision(null);
      setLoadState("idle");
      return;
    }
    const generation = ++generationRef.current;
    setCreatingEntry(false);
    setEditingEntryKey(null);
    setRevision(null);
    setLoadState("loading");
    try {
      const response = await client.get<CurrentRevisionResponse>(
        configPath(projectSlug, environmentSlug),
      );
      if (generationRef.current === generation) {
        setRevision(response.revision);
        setLoadState("ready");
      }
    } catch {
      if (generationRef.current === generation) {
        setLoadState("error");
      }
    }
  }, [client, environmentSlug, projectSlug]);

  useEffect(() => {
    if (skipOwnRefreshRef.current) {
      skipOwnRefreshRef.current = false;
      return;
    }
    void loadCurrent();
    return () => {
      generationRef.current += 1;
    };
  }, [loadCurrent, refreshEpoch]);

  useEffect(() => {
    const target = focusTargetRef.current;
    if (target === null) {
      return;
    }
    const frame = requestAnimationFrame(() => {
      configurationHeadingRef.current?.focus();
      focusTargetRef.current = null;
    });
    return () => cancelAnimationFrame(frame);
  }, [revision]);

  const visibleEntries = useMemo(() => {
    const query = search.toLocaleLowerCase();
    if (!query) {
      return revision?.entries ?? [];
    }
    return (revision?.entries ?? []).filter(
      (entry) =>
        entry.key.toLocaleLowerCase().includes(query) ||
        entry.service.toLocaleLowerCase().includes(query),
    );
  }, [revision, search]);
  const canEdit = canWrite;

  if (!environmentSlug) {
    return (
      <div className="empty-state compact-empty">
        <h2>{t("states.chooseEnvironment")}</h2>
        <p>{t("states.chooseEnvironmentDescription")}</p>
      </div>
    );
  }

  if (loadState === "loading" || loadState === "idle") {
    return <p className="loading-line" role="status">{t("states.loading")}</p>;
  }

  if (loadState === "error" || revision === null) {
    return (
      <div className="inline-error-state">
        <h2>{t("states.unavailable")}</h2>
        <p>{t("states.unavailableDescription")}</p>
        <button className="secondary-button" type="button" onClick={() => void loadCurrent()}>
          {t("common:actions.retry")}
        </button>
      </div>
    );
  }

  const register = (
    <section className="configuration-register" aria-labelledby="configuration-title">
      <header className="section-heading configuration-heading">
        <div>
          <p className="section-index">{t("table.index", { version: revision.version })}</p>
          <h2 ref={configurationHeadingRef} id="configuration-title" tabIndex={-1}>{t("table.title")}</h2>
          <p>{t("table.summary")}</p>
          {savedVersion !== null ? <p className="form-message" role="status">{t("table.saved", { version: savedVersion })}</p> : null}
        </div>
        {canEdit ? (
          <button
            className="primary-button"
            type="button"
            onClick={() => {
              setSavedVersion(null);
              setCreatingEntry(true);
            }}
          >
            {t("table.add")}
          </button>
        ) : null}
      </header>

      {revision.entries.length === 0 ? (
        <div className="empty-state compact-empty">
          <h3>{t("table.emptyTitle")}</h3>
          <p>{t(canEdit ? "table.emptyEditable" : "table.emptyReadOnly")}</p>
        </div>
      ) : (
        <>
          <div className="configuration-tools">
            <label htmlFor="configuration-search">{t("table.search")}</label>
            <input
              ref={searchInputRef}
              id="configuration-search"
              type="search"
              value={search}
              onChange={(event) => setSearch(event.currentTarget.value)}
            />
            {search ? (
              <button
                className="text-button"
                type="button"
                aria-label={t("table.clearSearch")}
                onClick={() => {
                  setSearch("");
                  searchInputRef.current?.focus();
                }}
              >
                {t("table.clearSearch")}
              </button>
            ) : null}
          </div>
          {visibleEntries.length === 0 ? (
            <p className="filter-empty">{t("table.noResults")}</p>
          ) : (
            <div className="data-table-wrap">
              <table className="data-table configuration-table" aria-label={t("table.accessibleName")}>
                <thead>
                  <tr>
                    <th scope="col">{t("table.key")}</th>
                    <th scope="col">{t("table.value")}</th>
                    <th scope="col">{t("table.service")}</th>
                    {canEdit ? <th scope="col">{t("table.actions")}</th> : null}
                  </tr>
                </thead>
                <tbody>
                  {visibleEntries.map((entry) => (
                    <tr key={entry.key}>
                      <th scope="row" data-label={t("table.key")}>
                        <OverflowText
                          emptyLabel={t("common:exactValue.emptyString")}
                          mono
                          testId={`configuration-key-${entry.key}`}
                          value={entry.key}
                        />
                      </th>
                      <td data-label={t("table.value")}>
                        <OverflowText
                          emptyLabel={t("common:exactValue.emptyString")}
                          mono
                          testId={`configuration-value-${entry.key}`}
                          value={entry.value}
                        />
                      </td>
                      <td data-label={t("table.service")}>
                        <OverflowText
                          emptyLabel={t("common:exactValue.emptyString")}
                          mono
                          testId={`configuration-service-${entry.key}`}
                          value={entry.service}
                        />
                      </td>
                      {canEdit ? (
                        <td data-label={t("table.actions")}>
                          <button
                            className="text-button configuration-row-action"
                            type="button"
                            aria-label={t("table.editEntry", { key: entry.key })}
                            onClick={() => setEditingEntryKey(entry.key)}
                          >
                            {t("table.editAction")}
                          </button>
                        </td>
                      ) : null}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </section>
  );

  const editingEntry = revision.entries.find((entry) => entry.key === editingEntryKey);
  const handleEntrySaved = (saved: Revision) => {
    skipOwnRefreshRef.current = true;
    focusTargetRef.current = "saved";
    setRevision(saved);
    setEditingEntryKey(null);
    setCreatingEntry(false);
    setSavedVersion(saved.version);
    onRevisionChanged(saved);
  };
  return (
    <>
      {register}
      {editingEntry ? (
        <ConfigEntryDialog
          client={client}
          entry={editingEntry}
          environmentSlug={environmentSlug}
          projectSlug={projectSlug}
          revision={revision}
          onCancel={() => setEditingEntryKey(null)}
          onSaved={handleEntrySaved}
        />
      ) : null}
      {creatingEntry ? (
        <ConfigEntryDialog
          client={client}
          entry={null}
          environmentSlug={environmentSlug}
          projectSlug={projectSlug}
          revision={revision}
          onCancel={() => setCreatingEntry(false)}
          onSaved={handleEntrySaved}
        />
      ) : null}
    </>
  );
}

function configPath(projectSlug: string, environmentSlug: string): string {
  return `/projects/${encodeURIComponent(projectSlug)}/environments/${encodeURIComponent(environmentSlug)}/config`;
}
