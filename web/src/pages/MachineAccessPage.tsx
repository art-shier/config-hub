import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import type {
  Environment,
  IssuedMachineToken,
  MachineEnvironmentGrant,
  MachineIdentity,
  MachineIdentityDetail,
  MachineTokenMetadata,
  Project,
  ProjectDetail,
} from "../api/types";
import type { APIClient } from "../api/client";
import { APIError } from "../api/client";
import { useAuth } from "../auth/AuthProvider";
import { ModalDialog } from "../components/ModalDialog";

type LoadState = "loading" | "ready" | "error";

interface ProjectOption extends Project {
  environments: Environment[];
}

export function MachineAccessPage() {
  const { client } = useAuth();
  const [identities, setIdentities] = useState<MachineIdentity[]>([]);
  const [identityState, setIdentityState] = useState<LoadState>("loading");
  const [selectedID, setSelectedID] = useState("");
  const [projects, setProjects] = useState<ProjectOption[]>([]);
  const [projectState, setProjectState] = useState<LoadState>("loading");
  const [createOpen, setCreateOpen] = useState(false);
  const identityGenerationRef = useRef(0);
  const projectGenerationRef = useRef(0);

  const loadIdentities = useCallback(async () => {
    const generation = ++identityGenerationRef.current;
    setIdentityState("loading");
    try {
      const response = await client.get<{ identities: MachineIdentity[] }>("/machine-identities");
      if (identityGenerationRef.current !== generation) {
        return;
      }
      if (!isIdentityList(response)) {
        throw new Error("invalid identity list");
      }
      setIdentities(response.identities);
      setSelectedID((current) =>
        response.identities.some((identity) => identity.id === current)
          ? current
          : (response.identities[0]?.id ?? ""),
      );
      setIdentityState("ready");
    } catch {
      if (identityGenerationRef.current === generation) {
        setIdentities([]);
        setSelectedID("");
        setIdentityState("error");
      }
    }
  }, [client]);

  const loadProjects = useCallback(async () => {
    const generation = ++projectGenerationRef.current;
    setProjectState("loading");
    try {
      const response = await client.get<{ projects: Project[] }>("/projects");
      if (!isProjectList(response)) {
        throw new Error("invalid project list");
      }
      const details = await Promise.all(
        response.projects.map(async (project) => {
          const detailResponse = await client.get<{ project: ProjectDetail }>(
            `/projects/${encodeURIComponent(project.slug)}`,
          );
          if (!isProjectDetailResponse(detailResponse) || detailResponse.project.id !== project.id) {
            throw new Error("invalid project detail");
          }
          return { ...project, environments: detailResponse.project.environments };
        }),
      );
      if (projectGenerationRef.current === generation) {
        setProjects(details);
        setProjectState("ready");
      }
    } catch {
      if (projectGenerationRef.current === generation) {
        setProjects([]);
        setProjectState("error");
      }
    }
  }, [client]);

  useEffect(() => {
    void loadIdentities();
    void loadProjects();
    return () => {
      identityGenerationRef.current += 1;
      projectGenerationRef.current += 1;
    };
  }, [loadIdentities, loadProjects]);

  function handleIdentityChanged(updated: MachineIdentity) {
    setIdentities((current) =>
      current.map((identity) => identity.id === updated.id ? updated : identity),
    );
  }

  function handleIdentityCreated(created: MachineIdentity) {
    setIdentities((current) => [...current, created].sort((left, right) => left.name.localeCompare(right.name)));
    setSelectedID(created.id);
    setCreateOpen(false);
  }

  return (
    <section className="resource-page machine-access-page" aria-labelledby="machine-access-title">
      <header className="resource-heading">
        <div>
          <p className="eyebrow">Scoped credential register</p>
          <h1 id="machine-access-title">Machine Access</h1>
          <p>Issue identities to builds, grant exact environments, and control one-time Tokens.</p>
        </div>
        <button className="primary-button action-button" type="button" onClick={() => setCreateOpen(true)}>
          New identity
        </button>
      </header>

      {identityState === "loading" ? <p className="loading-line" role="status">Loading machine identities…</p> : null}
      {identityState === "error" ? (
        <div className="inline-error-state administration-error" role="alert">
          <h2>Machine identities unavailable</h2>
          <p>The identity register couldn’t be loaded. Check the service and try again.</p>
          <button className="secondary-button" type="button" onClick={() => void loadIdentities()}>Retry identities</button>
        </div>
      ) : null}
      {identityState === "ready" && identities.length === 0 ? (
        <div className="empty-state">
          <p className="section-index">Identity register / Empty</p>
          <h2>No machine identities</h2>
          <p>Create an identity before granting environments or issuing a Token.</p>
        </div>
      ) : null}
      {identityState === "ready" && identities.length > 0 ? (
        <div className="machine-workspace">
          <aside className="machine-index" aria-label="Machine identities">
            <p className="nav-label">Identity register</p>
            <ul>
              {identities.map((identity) => (
                <li key={identity.id}>
                  <button
                    className={identity.id === selectedID ? "machine-index-button selected-machine" : "machine-index-button"}
                    type="button"
                    aria-pressed={identity.id === selectedID}
                    onClick={() => setSelectedID(identity.id)}
                  >
                    <strong>{identity.name}</strong>
                    <span>{identity.enabled ? "Enabled" : "Disabled"}</span>
                  </button>
                </li>
              ))}
            </ul>
          </aside>
          <IdentityPanel
            key={selectedID}
            client={client}
            identityID={selectedID}
            projects={projects}
            projectState={projectState}
            onRetryProjects={() => void loadProjects()}
            onIdentityChanged={handleIdentityChanged}
          />
        </div>
      ) : null}

      {createOpen ? (
        <CreateIdentityDialog client={client} onClose={() => setCreateOpen(false)} onCreated={handleIdentityCreated} />
      ) : null}
    </section>
  );
}

function IdentityPanel({
  client,
  identityID,
  onIdentityChanged,
  onRetryProjects,
  projects,
  projectState,
}: {
  client: APIClient;
  identityID: string;
  projects: ProjectOption[];
  projectState: LoadState;
  onRetryProjects(): void;
  onIdentityChanged(identity: MachineIdentity): void;
}) {
  const [detail, setDetail] = useState<MachineIdentityDetail | null>(null);
  const [state, setState] = useState<LoadState>("loading");
  const [description, setDescription] = useState("");
  const [enabled, setEnabled] = useState(false);
  const [grants, setGrants] = useState<MachineEnvironmentGrant[]>([]);
  const [projectID, setProjectID] = useState("");
  const [environmentID, setEnvironmentID] = useState("");
  const [identityMessage, setIdentityMessage] = useState("");
  const [identityError, setIdentityError] = useState("");
  const [grantMessage, setGrantMessage] = useState("");
  const [grantError, setGrantError] = useState("");
  const [savingIdentity, setSavingIdentity] = useState(false);
  const [savingGrants, setSavingGrants] = useState(false);
  const [issueOpen, setIssueOpen] = useState(false);
  const [viewToken, setViewToken] = useState<MachineTokenMetadata | null>(null);
  const [revokeToken, setRevokeToken] = useState<MachineTokenMetadata | null>(null);
  const generationRef = useRef(0);
  const identitySavingRef = useRef(false);
  const grantSavingRef = useRef(false);

  const loadDetail = useCallback(async () => {
    const generation = ++generationRef.current;
    setState("loading");
    try {
      const response = await client.get<{ identity: MachineIdentityDetail }>(
        `/machine-identities/${encodeURIComponent(identityID)}`,
      );
      if (generationRef.current !== generation) {
        return;
      }
      if (!isIdentityDetailResponse(response) || response.identity.id !== identityID) {
        throw new Error("invalid identity detail");
      }
      setDetail(response.identity);
      setDescription(response.identity.description);
      setEnabled(response.identity.enabled);
      setGrants(response.identity.grants);
      setState("ready");
    } catch {
      if (generationRef.current === generation) {
        setDetail(null);
        setState("error");
      }
    }
  }, [client, identityID]);

  useEffect(() => {
    void loadDetail();
    return () => {
      generationRef.current += 1;
    };
  }, [loadDetail]);

  useEffect(() => {
    const nextProjectID = projects.some((project) => project.id === projectID)
      ? projectID
      : (projects[0]?.id ?? "");
    setProjectID(nextProjectID);
    const environments = projects.find((project) => project.id === nextProjectID)?.environments ?? [];
    setEnvironmentID((current) => environments.some((environment) => environment.id === current) ? current : (environments[0]?.id ?? ""));
  }, [projectID, projects]);

  const selectedProject = projects.find((project) => project.id === projectID);

  async function saveIdentity(event: FormEvent) {
    event.preventDefault();
    if (detail === null || identitySavingRef.current) {
      return;
    }
    identitySavingRef.current = true;
    setSavingIdentity(true);
    setIdentityError("");
    setIdentityMessage("");
    try {
      const response = await client.put<{ identity: MachineIdentity }>(
        `/machine-identities/${encodeURIComponent(identityID)}`,
        { description, enabled },
      );
      if (!isIdentityResponse(response) || response.identity.id !== identityID) {
        throw new Error("invalid identity response");
      }
      setDetail((current) => current === null ? current : { ...current, ...response.identity });
      setDescription(response.identity.description);
      setEnabled(response.identity.enabled);
      onIdentityChanged(response.identity);
      setIdentityMessage("Identity saved.");
    } catch {
      setIdentityError("We couldn’t save the identity. Your changes are still here; check the service and try again.");
    } finally {
      identitySavingRef.current = false;
      setSavingIdentity(false);
    }
  }

  async function saveGrants() {
    if (grantSavingRef.current) {
      return;
    }
    grantSavingRef.current = true;
    setSavingGrants(true);
    setGrantError("");
    setGrantMessage("");
    try {
      await client.putNoContent(
        `/machine-identities/${encodeURIComponent(identityID)}/grants`,
        { grants },
      );
      setGrantMessage("Grants saved.");
    } catch {
      setGrantError("The grants couldn’t be saved. Your selections are still here; check the service and try again.");
    } finally {
      grantSavingRef.current = false;
      setSavingGrants(false);
    }
  }

  function addGrant() {
    if (!projectID || !environmentID || grants.some((grant) => grant.project_id === projectID && grant.environment_id === environmentID)) {
      return;
    }
    setGrantError("");
    setGrantMessage("");
    setGrants((current) => [...current, { project_id: projectID, environment_id: environmentID }]);
  }

  if (state === "loading") {
    return <p className="loading-line machine-detail-state" role="status">Loading identity details…</p>;
  }
  if (state === "error" || detail === null) {
    return (
      <div className="inline-error-state machine-detail-state" role="alert">
        <h2>Identity details unavailable</h2>
        <p>The selected identity couldn’t be loaded. Try again.</p>
        <button className="secondary-button" type="button" onClick={() => void loadDetail()}>Retry identity</button>
      </div>
    );
  }

  return (
    <article className="machine-detail" aria-labelledby="machine-detail-title">
      <header className="section-heading machine-detail-heading">
        <div>
          <p className="section-index">Machine identity / {detail.enabled ? "Enabled" : "Disabled"}</p>
          <h2 id="machine-detail-title">{detail.name}</h2>
          <p>Created {formatDateTime(detail.created_at)} · Updated {formatDateTime(detail.updated_at)}</p>
        </div>
      </header>

      <form className="machine-section machine-identity-form" onSubmit={(event) => void saveIdentity(event)}>
        <div className="section-heading compact-section-heading">
          <div>
            <p className="section-index">01 / Identity</p>
            <h3>Identity state</h3>
            <p>The immutable name anchors grants and Tokens.</p>
          </div>
        </div>
        <div className="form-field">
          <label htmlFor={`machine-description-${identityID}`}>Description</label>
          <textarea id={`machine-description-${identityID}`} maxLength={1024} value={description} disabled={savingIdentity} onChange={(event) => setDescription(event.currentTarget.value)} />
        </div>
        <label className="checkbox-field">
          <input type="checkbox" checked={enabled} disabled={savingIdentity} onChange={(event) => setEnabled(event.currentTarget.checked)} />
          <span>Enabled</span>
        </label>
        {identityError ? <p className="form-message" role="alert">{identityError}</p> : null}
        {identityMessage ? <p className="form-message" role="status">{identityMessage}</p> : null}
        <button className="secondary-button compact-control" type="submit" disabled={savingIdentity}>
          {savingIdentity ? "Saving identity…" : "Save identity"}
        </button>
      </form>

      <section className="machine-section" aria-labelledby={`grants-title-${identityID}`}>
        <div className="section-heading compact-section-heading">
          <div>
            <p className="section-index">02 / Grants</p>
            <h3 id={`grants-title-${identityID}`}>Environment grants</h3>
            <p>Each grant names one existing project and one environment.</p>
          </div>
        </div>
        {projectState === "loading" ? <p className="loading-line" role="status">Loading projects and environments…</p> : null}
        {projectState === "error" ? (
          <div className="inline-error-state compact-inline-error" role="alert">
            <p>Projects and environments couldn’t be loaded. Existing grant IDs remain unchanged.</p>
            <button className="secondary-button" type="button" onClick={onRetryProjects}>Retry grant options</button>
          </div>
        ) : null}
        {projectState === "ready" && projects.length === 0 ? (
          <p className="read-only-note">Create a project and environment before adding a grant.</p>
        ) : null}
        {projectState === "ready" && projects.length > 0 ? (
          <div className="grant-picker">
            <div className="form-field">
              <label htmlFor={`grant-project-${identityID}`}>Project</label>
              <select id={`grant-project-${identityID}`} value={projectID} onChange={(event) => setProjectID(event.currentTarget.value)}>
                {projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
              </select>
            </div>
            <div className="form-field">
              <label htmlFor={`grant-environment-${identityID}`}>Environment</label>
              <select id={`grant-environment-${identityID}`} value={environmentID} disabled={(selectedProject?.environments.length ?? 0) === 0} onChange={(event) => setEnvironmentID(event.currentTarget.value)}>
                {(selectedProject?.environments ?? []).map((environment) => <option key={environment.id} value={environment.id}>{environment.name}</option>)}
              </select>
            </div>
            <button className="secondary-button" type="button" disabled={!environmentID} onClick={addGrant}>Add grant</button>
          </div>
        ) : null}
        {grants.length === 0 ? <p className="read-only-note">This identity has no environment grants.</p> : (
          <ul className="grant-list">
            {grants.map((grant) => (
              <li key={`${grant.project_id}:${grant.environment_id}`}>
                <span>{grantLabel(projects, grant)}</span>
                <button className="text-button" type="button" disabled={savingGrants} onClick={() => setGrants((current) => current.filter((candidate) => candidate.project_id !== grant.project_id || candidate.environment_id !== grant.environment_id))}>
                  Remove grant
                </button>
              </li>
            ))}
          </ul>
        )}
        {grantError ? <p className="form-message" role="alert">{grantError}</p> : null}
        {grantMessage ? <p className="form-message" role="status">{grantMessage}</p> : null}
        <button className="secondary-button compact-control" type="button" disabled={savingGrants} onClick={() => void saveGrants()}>
          {savingGrants ? "Saving grants…" : "Save grants"}
        </button>
      </section>

      <section className="machine-section" aria-labelledby={`tokens-title-${identityID}`}>
        <div className="section-heading compact-section-heading">
          <div>
            <p className="section-index">03 / Tokens</p>
            <h3 id={`tokens-title-${identityID}`}>Token register</h3>
            <p>Plaintext appears once at issue time. The register keeps metadata only.</p>
          </div>
          <button className="secondary-button" type="button" disabled={!detail.enabled} onClick={() => setIssueOpen(true)}>Issue Token</button>
        </div>
        {detail.tokens.length === 0 ? <p className="read-only-note">No Tokens have been issued for this identity.</p> : (
          <div className="data-table-wrap token-table-wrap">
            <table className="data-table token-table" aria-label={`${detail.name} Tokens`}>
              <thead><tr><th scope="col">Token</th><th scope="col">Prefix</th><th scope="col">Expires</th><th scope="col">State</th><th scope="col">Actions</th></tr></thead>
              <tbody>
                {detail.tokens.map((token) => (
                  <tr key={token.id}>
                    <th scope="row">{token.name}</th>
                    <td><span className="code-label">{token.prefix}</span></td>
                    <td>{formatDateTime(token.expires_at)}</td>
                    <td><span className="state-label">{tokenState(token)}</span></td>
                    <td>
                      <div className="table-actions">
                        <button className="text-button" type="button" onClick={() => setViewToken(token)}>View {token.name} Token</button>
                        {token.revoked_at === null ? <button className="danger-button" type="button" onClick={() => setRevokeToken(token)}>Revoke {token.name} Token</button> : null}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {issueOpen ? (
        <IssueTokenDialog
          client={client}
          identityID={identityID}
          onClose={() => setIssueOpen(false)}
          onIssued={(metadata) => {
            setDetail((current) => current === null ? current : { ...current, tokens: [...current.tokens, metadata] });
          }}
        />
      ) : null}
      {viewToken ? <TokenMetadataDialog token={viewToken} onClose={() => setViewToken(null)} /> : null}
      {revokeToken ? (
        <RevokeTokenDialog
          client={client}
          identityID={identityID}
          token={revokeToken}
          onClose={() => setRevokeToken(null)}
          onRevoked={() => {
            setDetail((current) => current === null ? current : {
              ...current,
              tokens: current.tokens.map((token) => token.id === revokeToken.id ? { ...token, revoked_at: new Date().toISOString() } : token),
            });
            setRevokeToken(null);
          }}
        />
      ) : null}
    </article>
  );
}

function CreateIdentityDialog({ client, onClose, onCreated }: { client: APIClient; onClose(): void; onCreated(identity: MachineIdentity): void }) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const nameRef = useRef<HTMLInputElement>(null);
  const submittingRef = useRef(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (submittingRef.current) return;
    submittingRef.current = true;
    setSubmitting(true);
    setError("");
    setFieldErrors({});
    try {
      const response = await client.post<{ identity: MachineIdentity }>("/machine-identities", { name, description, enabled });
      if (!isIdentityResponse(response)) throw new Error("invalid identity response");
      onCreated(response.identity);
    } catch (caught) {
      if (caught instanceof APIError && caught.status === 422) setFieldErrors(caught.fields);
      setError("The identity couldn’t be created. Review the fields and try again.");
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  }

  return (
    <ModalDialog labelledBy="create-identity-title" describedBy="create-identity-description" initialFocusRef={nameRef} closeDisabled={submitting} onRequestClose={onClose}>
      <header className="dialog-heading"><div><p className="section-index">Machine access / New</p><h2 id="create-identity-title">New machine identity</h2><p id="create-identity-description">Create the durable identity before assigning grants or Tokens.</p></div></header>
      <form className="resource-form" onSubmit={(event) => void submit(event)}>
        <div className="form-field"><label htmlFor="machine-name">Machine name</label><input ref={nameRef} id="machine-name" required maxLength={128} value={name} disabled={submitting} aria-invalid={fieldErrors.name ? "true" : undefined} onChange={(event) => setName(event.currentTarget.value)} />{fieldErrors.name ? <p className="field-error">{fieldErrors.name}</p> : null}</div>
        <div className="form-field"><label htmlFor="machine-description">Description</label><textarea id="machine-description" maxLength={1024} value={description} disabled={submitting} onChange={(event) => setDescription(event.currentTarget.value)} /></div>
        <label className="checkbox-field"><input type="checkbox" checked={enabled} disabled={submitting} onChange={(event) => setEnabled(event.currentTarget.checked)} /><span>Enabled</span></label>
        {error ? <p role="alert">{error}</p> : null}
        <div className="dialog-actions"><button className="text-button" type="button" disabled={submitting} onClick={onClose}>Cancel</button><button className="primary-button" type="submit" disabled={submitting}>{submitting ? "Creating identity…" : "Create identity"}</button></div>
      </form>
    </ModalDialog>
  );
}

export function IssueTokenDialog({ client, identityID, onClose, onIssued }: { client: APIClient; identityID: string; onClose(): void; onIssued(token: MachineTokenMetadata): void }) {
  const [name, setName] = useState("");
  const [expiresAt, setExpiresAt] = useState(() => toDateTimeLocal(new Date(Date.now() + 30 * 24 * 60 * 60 * 1000)));
  const [issued, setIssued] = useState<IssuedMachineToken | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [copyError, setCopyError] = useState("");
  const nameRef = useRef<HTMLInputElement>(null);
  const submittingRef = useRef(false);
  const activeRef = useRef(true);

  useEffect(() => () => { activeRef.current = false; }, []);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (submittingRef.current) return;
    const expiry = new Date(expiresAt);
    if (!expiresAt || Number.isNaN(expiry.valueOf()) || expiry <= new Date() || expiry > new Date(Date.now() + 365 * 24 * 60 * 60 * 1000)) {
      setError("Choose an expiry in the future, no more than one year from now.");
      return;
    }
    submittingRef.current = true;
    setSubmitting(true);
    setError("");
    try {
      const response = await client.post<{ token: IssuedMachineToken }>(`/machine-identities/${encodeURIComponent(identityID)}/tokens`, { name, expires_at: expiry.toISOString() });
      if (!isIssuedTokenResponse(response)) throw new Error("invalid issued token response");
      if (activeRef.current) {
        setIssued(response.token);
        onIssued(metadataFromIssuedToken(response.token));
      }
    } catch {
      if (activeRef.current) setError("The Token couldn’t be issued. Your values are still here; check the expiry and try again.");
    } finally {
      submittingRef.current = false;
      if (activeRef.current) setSubmitting(false);
    }
  }

  async function copyToken() {
    if (issued === null) return;
    setCopyError("");
    try {
      if (!navigator.clipboard?.writeText) throw new Error("clipboard unavailable");
      await navigator.clipboard.writeText(issued.plaintext);
    } catch {
      setCopyError("Copy failed. The Token remains visible; select and copy it manually before closing.");
    }
  }

  return (
    <ModalDialog labelledBy="issue-token-title" describedBy="issue-token-description" initialFocusRef={issued === null ? nameRef : undefined} closeDisabled={submitting} onRequestClose={onClose}>
      <header className="dialog-heading"><div><p className="section-index">Machine access / One-time value</p><h2 id="issue-token-title">Issue Token</h2><p id="issue-token-description">After this dialog closes, ConfigHub cannot show the plaintext again.</p></div></header>
      {issued === null ? (
        <form className="resource-form" onSubmit={(event) => void submit(event)}>
          <div className="form-field"><label htmlFor="token-name">Token name</label><input ref={nameRef} id="token-name" required maxLength={128} value={name} disabled={submitting} onChange={(event) => setName(event.currentTarget.value)} /></div>
          <div className="form-field"><label htmlFor="token-expiry">Expires at</label><input id="token-expiry" type="datetime-local" required value={expiresAt} disabled={submitting} onChange={(event) => setExpiresAt(event.currentTarget.value)} /></div>
          {error ? <p role="alert">{error}</p> : null}
          <div className="dialog-actions"><button className="text-button" type="button" disabled={submitting} onClick={onClose}>Cancel</button><button className="primary-button" type="submit" disabled={submitting}>{submitting ? "Issuing Token…" : "Issue Token"}</button></div>
        </form>
      ) : (
        <div className="one-time-token">
          <p className="one-time-warning">Copy this Token now. Closing this dialog permanently removes it from the interface.</p>
          <output className="token-plaintext" aria-label="Issued Token">{issued.plaintext}</output>
          {copyError ? <p role="alert">{copyError}</p> : null}
          <div className="dialog-actions"><button className="secondary-button" type="button" onClick={() => void copyToken()}>Copy Token</button><button className="primary-button" type="button" onClick={onClose}>I have copied it</button></div>
        </div>
      )}
    </ModalDialog>
  );
}

function metadataFromIssuedToken(token: IssuedMachineToken): MachineTokenMetadata {
  return {
    id: token.id,
    name: token.name,
    prefix: token.prefix,
    created_at: token.created_at,
    expires_at: token.expires_at,
    revoked_at: null,
  };
}

function TokenMetadataDialog({ token, onClose }: { token: MachineTokenMetadata; onClose(): void }) {
  const closeRef = useRef<HTMLButtonElement>(null);
  return (
    <ModalDialog labelledBy="token-metadata-title" initialFocusRef={closeRef} onRequestClose={onClose}>
      <header className="dialog-heading"><div><p className="section-index">Token metadata / Read only</p><h2 id="token-metadata-title">{token.name} Token</h2></div></header>
      <dl className="metadata-ledger"><div><dt>Prefix</dt><dd className="code-label">{token.prefix}</dd></div><div><dt>Created</dt><dd>{formatDateTime(token.created_at)}</dd></div><div><dt>Expires</dt><dd>{formatDateTime(token.expires_at)}</dd></div><div><dt>State</dt><dd>{tokenState(token)}</dd></div></dl>
      <div className="dialog-actions"><button ref={closeRef} className="primary-button" type="button" onClick={onClose}>Close metadata</button></div>
    </ModalDialog>
  );
}

function RevokeTokenDialog({ client, identityID, token, onClose, onRevoked }: { client: APIClient; identityID: string; token: MachineTokenMetadata; onClose(): void; onRevoked(): void }) {
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const cancelRef = useRef<HTMLButtonElement>(null);
  const submittingRef = useRef(false);
  const activeRef = useRef(true);
  useEffect(() => () => { activeRef.current = false; }, []);

  async function revoke() {
    if (submittingRef.current) return;
    submittingRef.current = true;
    setSubmitting(true);
    setError("");
    try {
      await client.delete(`/machine-identities/${encodeURIComponent(identityID)}/tokens/${encodeURIComponent(token.id)}`);
      if (activeRef.current) onRevoked();
    } catch {
      if (activeRef.current) setError("The Token couldn’t be revoked. It remains active; check the service and try again.");
    } finally {
      submittingRef.current = false;
      if (activeRef.current) setSubmitting(false);
    }
  }

  return (
    <ModalDialog className="confirmation-panel" labelledBy="revoke-token-title" describedBy="revoke-token-description" initialFocusRef={cancelRef} closeDisabled={submitting} onRequestClose={onClose}>
      <p className="section-index">Token control / Confirmation</p>
      <h2 id="revoke-token-title">Revoke {token.name} Token?</h2>
      <p id="revoke-token-description">Builds using this Token will lose access immediately. This action cannot be undone.</p>
      {error ? <p className="confirmation-error" role="alert">{error}</p> : null}
      <div className="dialog-actions"><button ref={cancelRef} className="text-button" type="button" disabled={submitting} onClick={onClose}>Cancel</button><button className="danger-button" type="button" disabled={submitting} onClick={() => void revoke()}>{submitting ? "Revoking…" : "Revoke Token"}</button></div>
    </ModalDialog>
  );
}

function grantLabel(projects: ProjectOption[], grant: MachineEnvironmentGrant): string {
  const project = projects.find((candidate) => candidate.id === grant.project_id);
  const environment = project?.environments.find((candidate) => candidate.id === grant.environment_id);
  return project && environment ? `${project.name} / ${environment.name}` : `${grant.project_id} / ${grant.environment_id}`;
}

function tokenState(token: MachineTokenMetadata): "Active" | "Expired" | "Revoked" {
  if (token.revoked_at !== null) return "Revoked";
  const expiry = new Date(token.expires_at);
  return Number.isNaN(expiry.valueOf()) || expiry <= new Date() ? "Expired" : "Active";
}

function formatDateTime(value: string): string {
  const parsed = new Date(value);
  return Number.isNaN(parsed.valueOf()) ? "Unavailable" : new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(parsed);
}

function toDateTimeLocal(value: Date): string {
  const adjusted = new Date(value.valueOf() - value.getTimezoneOffset() * 60_000);
  return adjusted.toISOString().slice(0, 16);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isIdentity(value: unknown): value is MachineIdentity {
  return isRecord(value) && typeof value.id === "string" && typeof value.name === "string" && typeof value.description === "string" && typeof value.enabled === "boolean" && typeof value.created_at === "string" && typeof value.updated_at === "string";
}

function isIdentityList(value: unknown): value is { identities: MachineIdentity[] } {
  return isRecord(value) && Array.isArray(value.identities) && value.identities.every(isIdentity);
}

function isIdentityResponse(value: unknown): value is { identity: MachineIdentity } {
  return isRecord(value) && isIdentity(value.identity);
}

function isGrant(value: unknown): value is MachineEnvironmentGrant {
  return isRecord(value) && typeof value.project_id === "string" && typeof value.environment_id === "string";
}

function isToken(value: unknown): value is MachineTokenMetadata {
  return isRecord(value) && typeof value.id === "string" && typeof value.name === "string" && typeof value.prefix === "string" && typeof value.created_at === "string" && typeof value.expires_at === "string" && (value.revoked_at === null || typeof value.revoked_at === "string");
}

function isIdentityDetailResponse(value: unknown): value is { identity: MachineIdentityDetail } {
  if (!isRecord(value)) {
    return false;
  }
  const identity = value.identity;
  return isRecord(identity) && isIdentity(identity) && Array.isArray(identity.grants) && identity.grants.every(isGrant) && Array.isArray(identity.tokens) && identity.tokens.every(isToken);
}

function isIssuedTokenResponse(value: unknown): value is { token: IssuedMachineToken } {
  return isRecord(value) && isRecord(value.token) && typeof value.token.id === "string" && typeof value.token.name === "string" && typeof value.token.prefix === "string" && typeof value.token.plaintext === "string" && typeof value.token.expires_at === "string" && typeof value.token.created_at === "string";
}

function isProjectList(value: unknown): value is { projects: Project[] } {
  return isRecord(value) && Array.isArray(value.projects) && value.projects.every((project) => isRecord(project) && typeof project.id === "string" && typeof project.slug === "string" && typeof project.name === "string" && typeof project.description === "string" && typeof project.created_at === "string" && typeof project.updated_at === "string");
}

function isProjectDetailResponse(value: unknown): value is { project: ProjectDetail } {
  return isRecord(value) && isRecord(value.project) && typeof value.project.id === "string" && Array.isArray(value.project.environments) && value.project.environments.every((environment) => isRecord(environment) && typeof environment.id === "string" && typeof environment.project_id === "string" && typeof environment.slug === "string" && typeof environment.name === "string");
}
