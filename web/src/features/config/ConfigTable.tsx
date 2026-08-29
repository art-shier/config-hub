import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { APIClientContract, Revision } from "../../api/types";
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
  const [revision, setRevision] = useState<Revision | null>(null);
  const [loadState, setLoadState] = useState<LoadState>("idle");
  const [editing, setEditing] = useState(false);
  const [search, setSearch] = useState("");
  const generationRef = useRef(0);
  const skipOwnRefreshRef = useRef(false);

  const loadCurrent = useCallback(async () => {
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

  if (!environmentSlug) {
    return (
      <div className="empty-state compact-empty">
        <h2>Choose an environment</h2>
        <p>Select an environment above to view its current configuration.</p>
      </div>
    );
  }

  if (loadState === "loading" || loadState === "idle") {
    return <p className="loading-line" role="status">Loading configuration…</p>;
  }

  if (loadState === "error" || revision === null) {
    return (
      <div className="inline-error-state">
        <h2>Configuration unavailable</h2>
        <p>The current configuration couldn’t be loaded. Try again.</p>
        <button className="secondary-button" type="button" onClick={() => void loadCurrent()}>
          Retry
        </button>
      </div>
    );
  }

  if (editing) {
    return (
      <ConfigEditor
        client={client}
        projectSlug={projectSlug}
        environmentSlug={environmentSlug}
        revision={revision}
        onCancel={() => setEditing(false)}
        onSaved={(saved) => {
          skipOwnRefreshRef.current = true;
          setRevision(saved);
          setEditing(false);
          onRevisionChanged(saved);
        }}
      />
    );
  }

  return (
    <section className="configuration-register" aria-labelledby="configuration-title">
      <header className="section-heading configuration-heading">
        <div>
          <p className="section-index">Current register / Version {revision.version}</p>
          <h2 id="configuration-title">Configuration</h2>
          <p>Plain values are shown exactly as stored in this environment.</p>
        </div>
        {canWrite ? (
          <button className="secondary-button" type="button" onClick={() => setEditing(true)}>
            Edit configuration
          </button>
        ) : null}
      </header>

      {revision.entries.length === 0 ? (
        <div className="empty-state compact-empty">
          <h3>No configuration entries</h3>
          <p>{canWrite ? "Edit configuration to add the first entry." : "This environment has an empty configuration."}</p>
        </div>
      ) : (
        <>
          <div className="configuration-tools">
            <label htmlFor="configuration-search">Search configuration</label>
            <input
              id="configuration-search"
              type="search"
              value={search}
              onChange={(event) => setSearch(event.currentTarget.value)}
            />
          </div>
          {visibleEntries.length === 0 ? (
            <p className="filter-empty">No keys or services match this search.</p>
          ) : (
            <div className="data-table-wrap">
              <table className="data-table configuration-table" aria-label="Current configuration">
                <thead>
                  <tr>
                    <th scope="col">Key</th>
                    <th scope="col">Value</th>
                    <th scope="col">Service</th>
                  </tr>
                </thead>
                <tbody>
                  {visibleEntries.map((entry) => (
                    <tr key={entry.key}>
                      <th scope="row" data-label="Key"><span className="code-label">{entry.key}</span></th>
                      <td data-label="Value">
                        <span
                          className="configuration-value"
                          data-testid={`configuration-value-${entry.key}`}
                        >{entry.value}</span>
                      </td>
                      <td data-label="Service"><span className="code-label">{entry.service}</span></td>
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
}

function configPath(projectSlug: string, environmentSlug: string): string {
  return `/projects/${encodeURIComponent(projectSlug)}/environments/${encodeURIComponent(environmentSlug)}/config`;
}
