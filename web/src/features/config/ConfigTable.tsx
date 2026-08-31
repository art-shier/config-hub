import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { APIClientContract, Revision } from "../../api/types";
import { ExactValue } from "../../components/ExactValue";
import { ConfigEditor } from "./ConfigEditor";

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
  const [editing, setEditing] = useState(false);
  const [search, setSearch] = useState("");
  const [savedVersion, setSavedVersion] = useState<number | null>(null);
  const requiresDesktop = useMediaQuery("(max-width: 759px)");
  const generationRef = useRef(0);
  const skipOwnRefreshRef = useRef(false);
  const editButtonRef = useRef<HTMLButtonElement>(null);
  const configurationHeadingRef = useRef<HTMLHeadingElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const focusTargetRef = useRef<"editor" | "edit" | "saved" | null>(null);

  const loadCurrent = useCallback(async () => {
    setSavedVersion(null);
    if (!environmentSlug) {
      setRevision(null);
      setLoadState("idle");
      return;
    }
    const generation = ++generationRef.current;
    setEditing(false);
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
      const focusTarget = target === "editor"
        ? document.getElementById("configuration-editor-title")
        : target === "edit"
          ? editButtonRef.current
          : configurationHeadingRef.current;
      focusTarget?.focus();
      focusTargetRef.current = null;
    });
    return () => cancelAnimationFrame(frame);
  }, [editing, revision]);

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
  const canEdit = canWrite && !requiresDesktop;

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

  const editor = editing ? (
      <ConfigEditor
        client={client}
        projectSlug={projectSlug}
        environmentSlug={environmentSlug}
        revision={revision}
        editingUnavailable={requiresDesktop}
        onCancel={() => {
          focusTargetRef.current = "edit";
          setEditing(false);
        }}
        onSaved={(saved) => {
          skipOwnRefreshRef.current = true;
          focusTargetRef.current = "saved";
          setRevision(saved);
          setEditing(false);
          setSavedVersion(saved.version);
          onRevisionChanged(saved);
        }}
      />
  ) : null;

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
            ref={editButtonRef}
            className="secondary-button"
            type="button"
            onClick={() => {
              focusTargetRef.current = "editor";
              setSavedVersion(null);
              setEditing(true);
            }}
          >
            {t("table.edit")}
          </button>
        ) : null}
      </header>

      {canWrite && requiresDesktop ? (
        <p className="desktop-edit-note">
          {t(editing ? "table.desktopOnlyDraft" : "table.desktopOnly")}
        </p>
      ) : null}

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
                  </tr>
                </thead>
                <tbody>
                  {visibleEntries.map((entry) => (
                    <tr key={entry.key}>
                      <th scope="row" data-label={t("table.key")}><span className="code-label">{entry.key}</span></th>
                      <td data-label={t("table.value")}>
                        <ExactValue
                          label={t("table.storedValue", { key: entry.key })}
                          testId={`configuration-value-${entry.key}`}
                          value={entry.value}
                        />
                      </td>
                      <td data-label={t("table.service")}><span className="code-label">{entry.service}</span></td>
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

  if (editing) {
    return (
      <>
        {editor}
        {requiresDesktop ? register : null}
      </>
    );
  }

  return register;
}

function configPath(projectSlug: string, environmentSlug: string): string {
  return `/projects/${encodeURIComponent(projectSlug)}/environments/${encodeURIComponent(environmentSlug)}/config`;
}

function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() =>
    typeof window.matchMedia === "function" && window.matchMedia(query).matches,
  );

  useEffect(() => {
    if (typeof window.matchMedia !== "function") {
      return;
    }
    const media = window.matchMedia(query);
    const update = () => setMatches(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, [query]);

  return matches;
}
