import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import type {
  Environment,
  IssuedMachineToken,
  MachineEnvironmentGrant,
  MachineGrantPermission,
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
import { localizePresentFields } from "../i18n/apiErrors";
import { formatDateTime } from "../i18n/format";
import type { SupportedLocale } from "../i18n/locales";

type LoadState = "loading" | "ready" | "error";
type ValidationErrorKey =
  | "machineNameRequired"
  | "tokenNameRequired"
  | "machineNameTooLong"
  | "tokenNameTooLong"
  | "descriptionTooLong"
  | "expiryInvalid"
  | "expiryPast"
  | "expiryTooFar"
  | "serverName"
  | "serverTokenName"
  | "serverDescription"
  | "serverExpiry";
type MachineField = "name" | "description" | "expires_at";
type FieldErrors = Partial<Record<MachineField, ValidationErrorKey>>;
type IdentityErrorKey = "reviewField" | "saveFailed";
type GrantErrorKey = "invalid" | "saveFailed";
type CreateErrorKey = "reviewFields" | "fieldFailure" | "failure";
type IssueErrorKey = "reviewFields" | "fieldFailure" | "failure";
type TokenState = "active" | "expired" | "revoked";
type CopyState = "idle" | "copied" | "failed";

interface ProjectOption extends Project {
  environments: Environment[];
}

export function MachineAccessPage() {
  const { client } = useAuth();
  const { t } = useTranslation("machineAccess");
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
          <p className="eyebrow">{t("page.eyebrow")}</p>
          <h1 id="machine-access-title">{t("page.title")}</h1>
          <p>{t("page.summary")}</p>
        </div>
        <button className="primary-button action-button" type="button" onClick={() => setCreateOpen(true)}>
          {t("page.newIdentity")}
        </button>
      </header>

      {identityState === "loading" ? <p className="loading-line" role="status">{t("page.loading")}</p> : null}
      {identityState === "error" ? (
        <div className="inline-error-state administration-error" role="alert">
          <h2>{t("page.errorTitle")}</h2>
          <p>{t("page.errorDescription")}</p>
          <button className="secondary-button" type="button" onClick={() => void loadIdentities()}>{t("page.retry")}</button>
        </div>
      ) : null}
      {identityState === "ready" && identities.length === 0 ? (
        <div className="empty-state">
          <p className="section-index">{t("page.emptyIndex")}</p>
          <h2>{t("page.emptyTitle")}</h2>
          <p>{t("page.emptyDescription")}</p>
        </div>
      ) : null}
      {identityState === "ready" && identities.length > 0 ? (
        <div className="machine-workspace">
          <aside className="machine-index" aria-label={t("page.identitiesLabel")}>
            <p className="nav-label">{t("page.register")}</p>
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
                    <span>{t(identity.enabled ? "states.enabled" : "states.disabled")}</span>
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
  const { i18n, t } = useTranslation(["machineAccess", "common"]);
  const locale = resolvedLocale(i18n.resolvedLanguage);
  const [detail, setDetail] = useState<MachineIdentityDetail | null>(null);
  const [state, setState] = useState<LoadState>("loading");
  const [description, setDescription] = useState("");
  const [enabled, setEnabled] = useState(false);
  const [grants, setGrants] = useState<MachineEnvironmentGrant[]>([]);
  const [projectID, setProjectID] = useState("");
  const [environmentID, setEnvironmentID] = useState("");
  const [grantPermission, setGrantPermission] = useState<MachineGrantPermission>("read");
  const [identityMessage, setIdentityMessage] = useState<"saved" | null>(null);
  const [identityError, setIdentityError] = useState<IdentityErrorKey | null>(null);
  const [identityFieldErrors, setIdentityFieldErrors] = useState<FieldErrors>({});
  const [grantMessage, setGrantMessage] = useState<"saved" | null>(null);
  const [grantError, setGrantError] = useState<GrantErrorKey | null>(null);
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
    const descriptionError = validateDescription(description);
    setIdentityFieldErrors(descriptionError ? { description: descriptionError } : {});
    if (descriptionError) {
      setIdentityError("reviewField");
      setIdentityMessage(null);
      return;
    }
    identitySavingRef.current = true;
    setSavingIdentity(true);
    setIdentityError(null);
    setIdentityMessage(null);
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
      setIdentityFieldErrors({});
      onIdentityChanged(response.identity);
      setIdentityMessage("saved");
    } catch (caught) {
      if (caught instanceof APIError && caught.status === 422) {
        const mapped = mapFieldErrors(caught.fields, {
          description: "serverDescription",
        });
        setIdentityFieldErrors(mapped.fields);
        setIdentityError(
          Object.keys(mapped.fields).length > 0 && !mapped.hasUnknown
            ? "reviewField"
            : "saveFailed",
        );
      } else {
        setIdentityError("saveFailed");
      }
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
    setGrantError(null);
    setGrantMessage(null);
    try {
      await client.putNoContent(
        `/machine-identities/${encodeURIComponent(identityID)}/grants`,
        { grants },
      );
      setGrantMessage("saved");
    } catch (caught) {
      if (caught instanceof APIError && caught.status === 422) {
        const mapped = mapFieldErrors(caught.fields, { grants: "invalid" });
        setGrantError(
          mapped.fields.grants !== undefined && !mapped.hasUnknown
            ? mapped.fields.grants
            : "saveFailed",
        );
      } else {
        setGrantError("saveFailed");
      }
    } finally {
      grantSavingRef.current = false;
      setSavingGrants(false);
    }
  }

  function addGrant() {
    if (!projectID || !environmentID) {
      return;
    }
    setGrantError(null);
    setGrantMessage(null);
    setGrants((current) => {
      const nextGrant = {
        project_id: projectID,
        environment_id: environmentID,
        permission: grantPermission,
      };
      const existingIndex = current.findIndex(
        (grant) => grant.project_id === projectID && grant.environment_id === environmentID,
      );
      if (existingIndex === -1) {
        return [...current, nextGrant];
      }
      return current.map((grant, index) => index === existingIndex ? nextGrant : grant);
    });
  }

  if (state === "loading") {
    return <p className="loading-line machine-detail-state" role="status">{t("identity.loading")}</p>;
  }
  if (state === "error" || detail === null) {
    return (
      <div className="inline-error-state machine-detail-state" role="alert">
        <h2>{t("identity.errorTitle")}</h2>
        <p>{t("identity.errorDescription")}</p>
        <button className="secondary-button" type="button" onClick={() => void loadDetail()}>{t("identity.retry")}</button>
      </div>
    );
  }

  return (
    <article className="machine-detail" aria-labelledby="machine-detail-title">
      <header className="section-heading machine-detail-heading">
        <div>
          <p className="section-index">
            {t("identity.index", {
              state: t(detail.enabled ? "states.enabled" : "states.disabled"),
            })}
          </p>
          <h2 id="machine-detail-title">{detail.name}</h2>
          <p>
            {t("identity.dates", {
              created: formatDateTime(
                detail.created_at,
                locale,
                t("tokens.timeUnavailable"),
              ),
              updated: formatDateTime(
                detail.updated_at,
                locale,
                t("tokens.timeUnavailable"),
              ),
            })}
          </p>
        </div>
      </header>

      <form className="machine-section machine-identity-form" noValidate onSubmit={(event) => void saveIdentity(event)}>
        <div className="section-heading compact-section-heading">
          <div>
            <p className="section-index">{t("identity.sectionIndex")}</p>
            <h3>{t("identity.title")}</h3>
            <p>{t("identity.summary")}</p>
          </div>
        </div>
        <div className="form-field">
          <label htmlFor={`machine-description-${identityID}`}>{t("identity.description")}</label>
          <textarea
            className="resize-none"
            id={`machine-description-${identityID}`}
            value={description}
            style={{ resize: "none" }}
            disabled={savingIdentity}
            aria-invalid={identityFieldErrors.description ? "true" : undefined}
            aria-describedby={fieldDescription(
              `machine-description-${identityID}-help`,
              identityFieldErrors.description ? `machine-description-${identityID}-error` : undefined,
            )}
            onChange={(event) => {
              setDescription(event.currentTarget.value);
              setIdentityFieldErrors((current) => withoutField(current, "description"));
              setIdentityError(null);
            }}
          />
          <p className="field-help" id={`machine-description-${identityID}-help`}>
            {byteLimitHelp(t, description, machineDescriptionLimit)}
          </p>
          {identityFieldErrors.description ? (
            <p className="field-error" id={`machine-description-${identityID}-error`}>
              {validationMessage(t, identityFieldErrors.description)}
            </p>
          ) : null}
        </div>
        <label className="checkbox-field">
          <input type="checkbox" checked={enabled} disabled={savingIdentity} onChange={(event) => setEnabled(event.currentTarget.checked)} />
          <span>{t("identity.enabled")}</span>
        </label>
        {identityError ? <p className="form-message" role="alert">{t(`identity.${identityError}`)}</p> : null}
        {identityMessage ? <p className="form-message" role="status">{t(`identity.${identityMessage}`)}</p> : null}
        <button className="secondary-button compact-control" type="submit" disabled={savingIdentity}>
          {t(savingIdentity ? "identity.saving" : "identity.save")}
        </button>
      </form>

      <section className="machine-section" aria-labelledby={`grants-title-${identityID}`}>
        <div className="section-heading compact-section-heading">
          <div>
            <p className="section-index">{t("grants.sectionIndex")}</p>
            <h3 id={`grants-title-${identityID}`}>{t("grants.title")}</h3>
            <p>{t("grants.summary")}</p>
          </div>
        </div>
        {projectState === "loading" ? <p className="loading-line" role="status">{t("grants.loading")}</p> : null}
        {projectState === "error" ? (
          <div className="inline-error-state compact-inline-error" role="alert">
            <p>{t("grants.optionsError")}</p>
            <button className="secondary-button" type="button" onClick={onRetryProjects}>{t("grants.retryOptions")}</button>
          </div>
        ) : null}
        {projectState === "ready" && projects.length === 0 ? (
          <p className="read-only-note">{t("grants.noOptions")}</p>
        ) : null}
        {projectState === "ready" && projects.length > 0 ? (
          <div className="grant-picker">
            <div className="form-field">
              <label htmlFor={`grant-project-${identityID}`}>{t("grants.project")}</label>
              <select id={`grant-project-${identityID}`} value={projectID} onChange={(event) => setProjectID(event.currentTarget.value)}>
                {projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
              </select>
            </div>
            <div className="form-field">
              <label htmlFor={`grant-environment-${identityID}`}>{t("grants.environment")}</label>
              <select id={`grant-environment-${identityID}`} value={environmentID} disabled={(selectedProject?.environments.length ?? 0) === 0} onChange={(event) => setEnvironmentID(event.currentTarget.value)}>
                {(selectedProject?.environments ?? []).map((environment) => <option key={environment.id} value={environment.id}>{environment.name}</option>)}
              </select>
            </div>
            <div className="form-field">
              <label htmlFor={`grant-permission-${identityID}`}>{t("grants.permission")}</label>
              <select
                id={`grant-permission-${identityID}`}
                value={grantPermission}
                onChange={(event) => setGrantPermission(event.currentTarget.value as MachineGrantPermission)}
              >
                <option value="read">{t("grants.permissions.read")}</option>
                <option value="write">{t("grants.permissions.write")}</option>
              </select>
            </div>
            <button className="secondary-button" type="button" disabled={!environmentID} onClick={addGrant}>{t("grants.add")}</button>
          </div>
        ) : null}
        {grants.length === 0 ? <p className="read-only-note">{t("grants.empty")}</p> : (
          <ul className="grant-list">
            {grants.map((grant) => (
              <li key={`${grant.project_id}:${grant.environment_id}`}>
                <span>{grantLabel(t, projects, grant)}</span>
                <button className="text-button" type="button" aria-label={t("grants.removeLabel", { grant: grantLabel(t, projects, grant) })} disabled={savingGrants} onClick={() => setGrants((current) => current.filter((candidate) => candidate.project_id !== grant.project_id || candidate.environment_id !== grant.environment_id))}>
                  {t("grants.remove")}
                </button>
              </li>
            ))}
          </ul>
        )}
        {grantError ? <p className="form-message" role="alert">{t(`grants.${grantError}`)}</p> : null}
        {grantMessage ? <p className="form-message" role="status">{t(`grants.${grantMessage}`)}</p> : null}
        <button className="secondary-button compact-control" type="button" disabled={savingGrants} onClick={() => void saveGrants()}>
          {t(savingGrants ? "grants.saving" : "grants.save")}
        </button>
      </section>

      <section className="machine-section" aria-labelledby={`tokens-title-${identityID}`}>
        <div className="section-heading compact-section-heading">
          <div>
            <p className="section-index">{t("tokens.sectionIndex")}</p>
            <h3 id={`tokens-title-${identityID}`}>{t("tokens.title")}</h3>
            <p>{t("tokens.summary")}</p>
          </div>
          <button className="secondary-button" type="button" disabled={!detail.enabled} onClick={() => setIssueOpen(true)}>{t("tokens.issue")}</button>
        </div>
        {detail.tokens.length === 0 ? <p className="read-only-note">{t("tokens.empty")}</p> : (
          <div className="data-table-wrap token-table-wrap">
            <table className="data-table token-table" aria-label={t("tokens.tableLabel", { identity: detail.name })}>
              <thead><tr><th scope="col">{t("tokens.columns.token")}</th><th scope="col">{t("tokens.columns.prefix")}</th><th scope="col">{t("tokens.columns.expires")}</th><th scope="col">{t("tokens.columns.state")}</th><th scope="col">{t("tokens.columns.actions")}</th></tr></thead>
              <tbody>
                {detail.tokens.map((token) => (
                  <tr key={token.id}>
                    <th scope="row">{token.name}</th>
                    <td><span className="code-label">{token.prefix}</span></td>
                    <td>{formatDateTime(token.expires_at, locale, t("tokens.timeUnavailable"))}</td>
                    <td><span className="state-label">{t(`states.${tokenState(token)}`)}</span></td>
                    <td>
                      <div className="table-actions">
                        <button className="text-button" type="button" onClick={() => setViewToken(token)}>{t("tokens.view", { name: token.name })}</button>
                        {token.revoked_at === null ? <button className="danger-button" type="button" onClick={() => setRevokeToken(token)}>{t("tokens.revoke", { name: token.name })}</button> : null}
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
  const { t } = useTranslation(["machineAccess", "common"]);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<CreateErrorKey | null>(null);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const nameRef = useRef<HTMLInputElement>(null);
  const submittingRef = useRef(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (submittingRef.current) return;
    const localErrors = validateIdentity(name, description);
    setFieldErrors(localErrors);
    if (Object.keys(localErrors).length > 0) {
      setError("reviewFields");
      return;
    }
    submittingRef.current = true;
    setSubmitting(true);
    setError(null);
    setFieldErrors({});
    try {
      const response = await client.post<{ identity: MachineIdentity }>("/machine-identities", { name, description, enabled });
      if (!isIdentityResponse(response)) throw new Error("invalid identity response");
      onCreated(response.identity);
    } catch (caught) {
      if (caught instanceof APIError && caught.status === 422) {
        const mapped = mapFieldErrors(caught.fields, {
          name: "serverName",
          description: "serverDescription",
        });
        setFieldErrors(mapped.fields);
        setError(
          Object.keys(mapped.fields).length > 0 && !mapped.hasUnknown
            ? "fieldFailure"
            : "failure",
        );
      } else {
        setError("failure");
      }
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  }

  return (
    <ModalDialog labelledBy="create-identity-title" describedBy="create-identity-description" initialFocusRef={nameRef} closeDisabled={submitting} onRequestClose={onClose}>
      <header className="dialog-heading"><div><p className="section-index">{t("create.index")}</p><h2 id="create-identity-title">{t("create.title")}</h2><p id="create-identity-description">{t("create.description")}</p></div></header>
      <form className="resource-form" noValidate onSubmit={(event) => void submit(event)}>
        <div className="form-field">
          <label htmlFor="machine-name">{t("create.name")}</label>
          <input
            ref={nameRef}
            id="machine-name"
            required
            value={name}
            disabled={submitting}
            aria-invalid={fieldErrors.name ? "true" : undefined}
            aria-describedby={fieldDescription("machine-name-help", fieldErrors.name ? "machine-name-error" : undefined)}
            onChange={(event) => {
              setName(event.currentTarget.value);
              setFieldErrors((current) => withoutField(current, "name"));
              setError(null);
            }}
          />
          <p className="field-help" id="machine-name-help">{byteLimitHelp(t, name, machineNameLimit)}</p>
          {fieldErrors.name ? <p className="field-error" id="machine-name-error">{validationMessage(t, fieldErrors.name)}</p> : null}
        </div>
        <div className="form-field">
          <label htmlFor="machine-description">{t("create.descriptionLabel")}</label>
          <textarea
            className="resize-none"
            id="machine-description"
            value={description}
            style={{ resize: "none" }}
            disabled={submitting}
            aria-invalid={fieldErrors.description ? "true" : undefined}
            aria-describedby={fieldDescription("machine-description-help", fieldErrors.description ? "machine-description-error" : undefined)}
            onChange={(event) => {
              setDescription(event.currentTarget.value);
              setFieldErrors((current) => withoutField(current, "description"));
              setError(null);
            }}
          />
          <p className="field-help" id="machine-description-help">{byteLimitHelp(t, description, machineDescriptionLimit)}</p>
          {fieldErrors.description ? <p className="field-error" id="machine-description-error">{validationMessage(t, fieldErrors.description)}</p> : null}
        </div>
        <label className="checkbox-field"><input type="checkbox" checked={enabled} disabled={submitting} onChange={(event) => setEnabled(event.currentTarget.checked)} /><span>{t("create.enabled")}</span></label>
        {error ? <p role="alert">{t(`create.${error}`)}</p> : null}
        <div className="dialog-actions"><button className="text-button" type="button" disabled={submitting} onClick={onClose}>{t("common:actions.cancel")}</button><button className="primary-button" type="submit" disabled={submitting}>{t(submitting ? "create.pending" : "create.action")}</button></div>
      </form>
    </ModalDialog>
  );
}

export function IssueTokenDialog({ client, identityID, onClose, onIssued }: { client: APIClient; identityID: string; onClose(): void; onIssued(token: MachineTokenMetadata): void }) {
  const { t } = useTranslation(["machineAccess", "common"]);
  const [name, setName] = useState("");
  const [expiresAt, setExpiresAt] = useState(() => toDateTimeLocal(new Date(Date.now() + 30 * 24 * 60 * 60 * 1000)));
  const [issued, setIssued] = useState<IssuedMachineToken | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<IssueErrorKey | null>(null);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [copyState, setCopyState] = useState<CopyState>("idle");
  const nameRef = useRef<HTMLInputElement>(null);
  const submittingRef = useRef(false);
  const activeRef = useRef(true);

  useEffect(() => () => { activeRef.current = false; }, []);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (submittingRef.current) return;
    const expiry = new Date(expiresAt);
    const localErrors: FieldErrors = {};
    const nameError = validateName(name, "token");
    const expiryError = validateExpiry(expiresAt, expiry);
    if (nameError) localErrors.name = nameError;
    if (expiryError) localErrors.expires_at = expiryError;
    setFieldErrors(localErrors);
    if (Object.keys(localErrors).length > 0) {
      setError("reviewFields");
      return;
    }
    submittingRef.current = true;
    setSubmitting(true);
    setError(null);
    try {
      const response = await client.post<{ token: IssuedMachineToken }>(`/machine-identities/${encodeURIComponent(identityID)}/tokens`, { name, expires_at: expiry.toISOString() });
      if (!isIssuedTokenResponse(response)) throw new Error("invalid issued token response");
      if (activeRef.current) {
        setIssued(response.token);
        onIssued(metadataFromIssuedToken(response.token));
      }
    } catch (caught) {
      if (activeRef.current) {
        if (caught instanceof APIError && caught.status === 422) {
          const mapped = mapFieldErrors(caught.fields, {
            name: "serverTokenName",
            expires_at: "serverExpiry",
          });
          setFieldErrors(mapped.fields);
          setError(
            Object.keys(mapped.fields).length > 0 && !mapped.hasUnknown
              ? "fieldFailure"
              : "failure",
          );
        } else {
          setError("failure");
        }
      }
    } finally {
      submittingRef.current = false;
      if (activeRef.current) setSubmitting(false);
    }
  }

  async function copyToken() {
    if (issued === null) return;
    setCopyState("idle");
    try {
      if (!navigator.clipboard?.writeText) throw new Error("clipboard unavailable");
      await navigator.clipboard.writeText(issued.plaintext);
      if (activeRef.current) setCopyState("copied");
    } catch {
      if (activeRef.current) setCopyState("failed");
    }
  }

  return (
    <ModalDialog labelledBy="issue-token-title" describedBy="issue-token-description" initialFocusRef={issued === null ? nameRef : undefined} closeDisabled={submitting} onRequestClose={onClose}>
      <header className="dialog-heading"><div><p className="section-index">{t("issue.index")}</p><h2 id="issue-token-title">{t("issue.title")}</h2><p id="issue-token-description">{t("issue.description")}</p></div></header>
      {issued === null ? (
        <form className="resource-form" noValidate onSubmit={(event) => void submit(event)}>
          <div className="form-field">
            <label htmlFor="token-name">{t("issue.name")}</label>
            <input
              ref={nameRef}
              id="token-name"
              required
              value={name}
              disabled={submitting}
              aria-invalid={fieldErrors.name ? "true" : undefined}
              aria-describedby={fieldDescription("token-name-help", fieldErrors.name ? "token-name-error" : undefined)}
              onChange={(event) => {
                setName(event.currentTarget.value);
                setFieldErrors((current) => withoutField(current, "name"));
                setError(null);
              }}
            />
            <p className="field-help" id="token-name-help">{byteLimitHelp(t, name, machineNameLimit)}</p>
            {fieldErrors.name ? <p className="field-error" id="token-name-error">{validationMessage(t, fieldErrors.name)}</p> : null}
          </div>
          <div className="form-field">
            <label htmlFor="token-expiry">{t("issue.expiry")}</label>
            <input
              id="token-expiry"
              type="datetime-local"
              required
              value={expiresAt}
              disabled={submitting}
              aria-invalid={fieldErrors.expires_at ? "true" : undefined}
              aria-describedby={fieldErrors.expires_at ? "token-expiry-error" : undefined}
              onChange={(event) => {
                setExpiresAt(event.currentTarget.value);
                setFieldErrors((current) => withoutField(current, "expires_at"));
                setError(null);
              }}
            />
            {fieldErrors.expires_at ? <p className="field-error" id="token-expiry-error">{validationMessage(t, fieldErrors.expires_at)}</p> : null}
          </div>
          {error ? <p role="alert">{t(`issue.${error}`)}</p> : null}
          <div className="dialog-actions"><button className="text-button" type="button" disabled={submitting} onClick={onClose}>{t("common:actions.cancel")}</button><button className="primary-button" type="submit" disabled={submitting}>{t(submitting ? "issue.pending" : "issue.action")}</button></div>
        </form>
      ) : (
        <div className="one-time-token">
          <p className="one-time-warning">{t("issue.warning")}</p>
          <output className="token-plaintext" aria-label={t("issue.issuedLabel")}>{issued.plaintext}</output>
          {copyState === "failed" ? <p role="alert">{t("issue.copyFailed")}</p> : null}
          {copyState === "copied" ? <p role="status">{t("issue.copiedStatus")}</p> : null}
          <div className="dialog-actions"><button className="secondary-button" type="button" onClick={() => void copyToken()}>{t(copyState === "copied" ? "issue.copied" : "issue.copy")}</button><button className="primary-button" type="button" onClick={onClose}>{t("issue.dismiss")}</button></div>
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
  const { i18n, t } = useTranslation("machineAccess");
  const locale = resolvedLocale(i18n.resolvedLanguage);
  const closeRef = useRef<HTMLButtonElement>(null);
  return (
    <ModalDialog labelledBy="token-metadata-title" initialFocusRef={closeRef} onRequestClose={onClose}>
      <header className="dialog-heading"><div><p className="section-index">{t("metadata.index")}</p><h2 id="token-metadata-title">{t("metadata.title", { name: token.name })}</h2></div></header>
      <dl className="metadata-ledger"><div><dt>{t("metadata.prefix")}</dt><dd className="code-label">{token.prefix}</dd></div><div><dt>{t("metadata.created")}</dt><dd>{formatDateTime(token.created_at, locale, t("tokens.timeUnavailable"))}</dd></div><div><dt>{t("metadata.expires")}</dt><dd>{formatDateTime(token.expires_at, locale, t("tokens.timeUnavailable"))}</dd></div><div><dt>{t("metadata.state")}</dt><dd>{t(`states.${tokenState(token)}`)}</dd></div></dl>
      <div className="dialog-actions"><button ref={closeRef} className="primary-button" type="button" onClick={onClose}>{t("metadata.close")}</button></div>
    </ModalDialog>
  );
}

function RevokeTokenDialog({ client, identityID, token, onClose, onRevoked }: { client: APIClient; identityID: string; token: MachineTokenMetadata; onClose(): void; onRevoked(): void }) {
  const { t } = useTranslation(["machineAccess", "common"]);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState(false);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const submittingRef = useRef(false);
  const activeRef = useRef(true);
  useEffect(() => () => { activeRef.current = false; }, []);

  async function revoke() {
    if (submittingRef.current) return;
    submittingRef.current = true;
    setSubmitting(true);
    setError(false);
    try {
      await client.delete(`/machine-identities/${encodeURIComponent(identityID)}/tokens/${encodeURIComponent(token.id)}`);
      if (activeRef.current) onRevoked();
    } catch {
      if (activeRef.current) setError(true);
    } finally {
      submittingRef.current = false;
      if (activeRef.current) setSubmitting(false);
    }
  }

  return (
    <ModalDialog className="confirmation-panel" labelledBy="revoke-token-title" describedBy="revoke-token-description" initialFocusRef={cancelRef} closeDisabled={submitting} onRequestClose={onClose}>
      <p className="section-index">{t("revoke.index")}</p>
      <h2 id="revoke-token-title">{t("revoke.title", { name: token.name })}</h2>
      <p id="revoke-token-description">{t("revoke.description")}</p>
      {error ? <p className="confirmation-error" role="alert">{t("revoke.failure")}</p> : null}
      <div className="dialog-actions"><button ref={cancelRef} className="text-button" type="button" disabled={submitting} onClick={onClose}>{t("common:actions.cancel")}</button><button className="danger-button" type="button" disabled={submitting} onClick={() => void revoke()}>{t(submitting ? "revoke.pending" : "revoke.action")}</button></div>
    </ModalDialog>
  );
}

const machineNameLimit = 128;
const machineDescriptionLimit = 1024;
const maxTokenLifetimeMilliseconds = 365 * 24 * 60 * 60 * 1000;

function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(trimGoSpace(value)).byteLength;
}

const goSpaceEdges = /^[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+|[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+$/gu;

function trimGoSpace(value: string): string {
  return value.replace(goSpaceEdges, "");
}

function byteLimitHelp(t: TFunction, value: string, limit: number): string {
  return t("validation.byteLimit", { count: utf8ByteLength(value), limit });
}

function validateName(
  value: string,
  kind: "machine" | "token",
): ValidationErrorKey | null {
  const bytes = utf8ByteLength(value);
  if (trimGoSpace(value) === "") {
    return kind === "machine" ? "machineNameRequired" : "tokenNameRequired";
  }
  return bytes > machineNameLimit
    ? kind === "machine" ? "machineNameTooLong" : "tokenNameTooLong"
    : null;
}

function validateDescription(value: string): ValidationErrorKey | null {
  return utf8ByteLength(value) > machineDescriptionLimit
    ? "descriptionTooLong"
    : null;
}

function validateIdentity(name: string, description: string): FieldErrors {
  const fields: FieldErrors = {};
  const nameError = validateName(name, "machine");
  const descriptionError = validateDescription(description);
  if (nameError) fields.name = nameError;
  if (descriptionError) fields.description = descriptionError;
  return fields;
}

function validateExpiry(value: string, expiry: Date): ValidationErrorKey | null {
  const now = Date.now();
  if (!value || Number.isNaN(expiry.valueOf())) {
    return "expiryInvalid";
  }
  if (expiry.valueOf() <= now) {
    return "expiryPast";
  }
  return expiry.valueOf() > now + maxTokenLifetimeMilliseconds
    ? "expiryTooFar"
    : null;
}

function validationMessage(t: TFunction, key: ValidationErrorKey): string {
  return t(`validation.${key}`, {
    limit: key === "descriptionTooLong" ? machineDescriptionLimit : machineNameLimit,
  });
}

function fieldDescription(helpID: string, errorID?: string): string {
  return errorID ? `${helpID} ${errorID}` : helpID;
}

function withoutField(fields: FieldErrors, field: MachineField): FieldErrors {
  if (!(field in fields)) {
    return fields;
  }
  const next = { ...fields };
  delete next[field];
  return next;
}

function mapFieldErrors<K extends string, V extends string>(
  fields: Record<string, string>,
  messages: Record<K, V>,
): { fields: Partial<Record<K, V>>; hasUnknown: boolean } {
  const mapped = localizePresentFields(fields, messages);
  return {
    fields: mapped as Partial<Record<K, V>>,
    hasUnknown: Object.keys(fields).some((field) => !Object.hasOwn(messages, field)),
  };
}

function grantLabel(t: TFunction, projects: ProjectOption[], grant: MachineEnvironmentGrant): string {
  const project = projects.find((candidate) => candidate.id === grant.project_id);
  const environment = project?.environments.find((candidate) => candidate.id === grant.environment_id);
  return t("grants.label", {
    project: project?.name ?? grant.project_id,
    environment: environment?.name ?? grant.environment_id,
    permission: t(`grants.permissions.${grant.permission}`),
  });
}

function tokenState(token: MachineTokenMetadata): TokenState {
  if (token.revoked_at !== null) return "revoked";
  const expiry = new Date(token.expires_at);
  return Number.isNaN(expiry.valueOf()) || expiry <= new Date() ? "expired" : "active";
}

function resolvedLocale(language: string | undefined): SupportedLocale {
  return language === "zh-CN" ? "zh-CN" : "en-US";
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
  return isRecord(value)
    && typeof value.project_id === "string"
    && typeof value.environment_id === "string"
    && (value.permission === "read" || value.permission === "write");
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
