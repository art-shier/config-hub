import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { APIError } from "../api/client";
import type {
  Environment as ProjectEnvironment,
  ProjectDetail,
  Revision,
} from "../api/types";
import { useAuth } from "../auth/AuthProvider";
import { ModalDialog } from "../components/ModalDialog";
import { ConfigTable } from "../features/config/ConfigTable";
import { ProjectMembers } from "../features/members/ProjectMembers";
import { VersionList } from "../features/versions/VersionList";
import { localizePresentFields } from "../i18n/apiErrors";
import { formatDate } from "../i18n/format";
import type { SupportedLocale } from "../i18n/locales";

interface ProjectDetailResponse {
  project: ProjectDetail;
}

interface CreateEnvironmentResponse {
  environment: ProjectEnvironment;
}

type ProjectTab = "configuration" | "versions" | "members";
type LoadFailure = "not-found" | "unavailable" | null;

const projectSlugPattern = /^[a-z0-9][a-z0-9-]{0,62}$/u;
const projectTabs: ProjectTab[] = ["configuration", "versions", "members"];

export function ProjectPage() {
  const { client, user } = useAuth();
  const { i18n, t } = useTranslation("projects");
  const locale: SupportedLocale =
    i18n.resolvedLanguage === "zh-CN" ? "zh-CN" : "en-US";
  const { project: projectSlug = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const [project, setProject] = useState<ProjectDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<LoadFailure>(null);
  const [loadAnnouncement, setLoadAnnouncement] = useState<
    | "loadingProject"
    | "projectLoaded"
    | "projectNotFound"
    | "projectUnavailable"
  >("loadingProject");
  const [creatingEnvironment, setCreatingEnvironment] = useState(false);
  const [revisionRefreshEpoch, setRevisionRefreshEpoch] = useState(0);
  const newEnvironmentButtonRef = useRef<HTMLButtonElement>(null);
  const loadGenerationRef = useRef(0);

  const loadProject = useCallback(async () => {
    const generation = ++loadGenerationRef.current;
    setProject(null);
    setFailure(null);
    setLoading(true);
    setLoadAnnouncement("loadingProject");
    if (!projectSlugPattern.test(projectSlug)) {
      setLoading(false);
      setFailure("not-found");
      setLoadAnnouncement("projectNotFound");
      return;
    }
    try {
      const response = await client.get<ProjectDetailResponse>(
        `/projects/${encodeURIComponent(projectSlug)}`,
      );
      if (loadGenerationRef.current === generation) {
        setProject(response.project);
        setLoadAnnouncement("projectLoaded");
      }
    } catch (error) {
      if (loadGenerationRef.current === generation) {
        const nextFailure =
          error instanceof APIError && error.status === 404
            ? "not-found"
            : "unavailable";
        setFailure(nextFailure);
        setLoadAnnouncement(
          nextFailure === "not-found"
            ? "projectNotFound"
            : "projectUnavailable",
        );
      }
    } finally {
      if (loadGenerationRef.current === generation) {
        setLoading(false);
      }
    }
  }, [client, projectSlug]);

  useEffect(() => {
    void loadProject();
    return () => {
      loadGenerationRef.current += 1;
    };
  }, [loadProject]);

  const activeTab = safeTab(searchParams.get("tab"));
  const requestedEnvironment = searchParams.get("environment");
  const selectedEnvironment = project
    ? project.environments.some(
        (environment) => environment.slug === requestedEnvironment,
      )
      ? (requestedEnvironment ?? "")
      : (project.environments[0]?.slug ?? "")
    : "";

  useEffect(() => {
    if (project === null) {
      return;
    }
    const next = approvedSearch(selectedEnvironment, activeTab);
    if (next.toString() !== searchParams.toString()) {
      setSearchParams(next, { replace: true });
    }
  }, [activeTab, project, searchParams, selectedEnvironment, setSearchParams]);

  function closeEnvironmentCreation() {
    setCreatingEnvironment(false);
    window.requestAnimationFrame(() =>
      newEnvironmentButtonRef.current?.focus(),
    );
  }

  function addEnvironment(environment: ProjectEnvironment) {
    setProject((current) =>
      current === null
        ? current
        : {
            ...current,
            environments: [
              ...current.environments.filter(
                (item) =>
                  item.id !== environment.id && item.slug !== environment.slug,
              ),
              environment,
            ].sort((left, right) => left.slug.localeCompare(right.slug)),
          },
    );
  }

  function handleRevisionChanged(revision: Revision) {
    setProject((current) =>
      current === null
        ? current
        : {
            ...current,
            environments: current.environments.map((environment) =>
              environment.slug === selectedEnvironment
                ? { ...environment, current_revision_id: revision.id || null }
                : environment,
            ),
          },
    );
    setRevisionRefreshEpoch((current) => current + 1);
  }

  if (loading) {
    return (
      <section className="resource-page">
        <p className="loading-line" role="status">
          {t(`states.${loadAnnouncement}`)}
        </p>
      </section>
    );
  }
  if (failure !== null || project === null) {
    const notFound = failure === "not-found";
    return (
      <section className="resource-page page-error-state">
        <p className="eyebrow">{t("detail.projectIndex")}</p>
        <h1>
          {notFound ? t("detail.notFoundTitle") : t("detail.unavailableTitle")}
        </h1>
        <p>
          {notFound
            ? t("errors.projectNotFound")
            : t("errors.projectUnavailable")}
        </p>
        <button
          className="secondary-button"
          type="button"
          onClick={() => void loadProject()}
        >
          {t("actions.retry")}
        </button>
        <Link className="text-link" to="/projects">
          {t("actions.returnToProjects")}
        </Link>
      </section>
    );
  }

  const canManage = user?.role === "admin" && project.permission === "admin";
  const canWrite =
    project.permission === "admin" || project.permission === "editor";
  const selectedEnvironmentRecord = project.environments.find(
    (environment) => environment.slug === selectedEnvironment,
  );

  function handleTabKeyDown(
    event: React.KeyboardEvent<HTMLAnchorElement>,
    currentIndex: number,
  ) {
    let nextIndex: number;
    switch (event.key) {
      case "ArrowRight":
        nextIndex = (currentIndex + 1) % projectTabs.length;
        break;
      case "ArrowLeft":
        nextIndex =
          (currentIndex - 1 + projectTabs.length) % projectTabs.length;
        break;
      case "Home":
        nextIndex = 0;
        break;
      case "End":
        nextIndex = projectTabs.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    const nextTab = projectTabs[nextIndex];
    setSearchParams(approvedSearch(selectedEnvironment, nextTab));
    document.getElementById(`project-tab-${nextTab}`)?.focus();
  }

  return (
    <article
      className="resource-page project-workspace"
      aria-labelledby="project-title"
    >
      <div className="sr-status" aria-live="polite" aria-atomic="true">
        {t(`states.${loadAnnouncement}`)}
      </div>
      <Link className="back-link" to="/projects">
        {t("detail.back")}
      </Link>
      <header className="project-heading">
        <div>
          <p className="eyebrow">
            {t("detail.eyebrow", { slug: project.slug })}
          </p>
          <h1 id="project-title">{project.name}</h1>
          <p>{project.description || t("detail.noDescription")}</p>
        </div>
        <dl className="project-facts">
          <div>
            <dt>{t("detail.slug")}</dt>
            <dd className="code-label">{project.slug}</dd>
          </div>
          <div>
            <dt>{t("detail.access")}</dt>
            <dd>{t(`permissions.${project.permission}`)}</dd>
          </div>
          <div>
            <dt>{t("detail.updated")}</dt>
            <dd>
              {formatDate(project.updated_at, locale, t("states.unavailable"))}
            </dd>
          </div>
        </dl>
      </header>

      <section
        className="environment-section"
        aria-labelledby="environments-title"
      >
        <header className="section-heading">
          <div>
            <p className="section-index">{t("detail.environmentIndex")}</p>
            <h2 id="environments-title">{t("detail.environments")}</h2>
            <p>{t("detail.environmentSummary")}</p>
          </div>
          {canManage ? (
            <button
              ref={newEnvironmentButtonRef}
              className="primary-button action-button"
              type="button"
              onClick={() => setCreatingEnvironment(true)}
            >
              {t("detail.createEnvironment")}
            </button>
          ) : null}
        </header>

        {project.environments.length === 0 ? (
          <div className="empty-state compact-empty">
            <h3>{t("empty.environmentTitle")}</h3>
            <p>
              {canManage
                ? t("empty.adminEnvironment")
                : t("empty.memberEnvironment")}
            </p>
          </div>
        ) : (
          <>
            <div className="environment-picker">
              <label htmlFor="active-environment">
                {t("detail.activeEnvironment")}
              </label>
              <select
                id="active-environment"
                value={selectedEnvironment}
                onChange={(event) => {
                  setSearchParams(
                    approvedSearch(event.currentTarget.value, activeTab),
                  );
                }}
              >
                {project.environments.map((environment) => (
                  <option key={environment.id} value={environment.slug}>
                    {environment.name}
                  </option>
                ))}
              </select>
            </div>
            <ul
              className="environment-list"
              aria-label={t("detail.environmentList")}
            >
              {project.environments.map((environment) => (
                <li
                  key={environment.id}
                  className={
                    environment.slug === selectedEnvironment
                      ? "environment-active"
                      : undefined
                  }
                >
                  <div>
                    <strong>{environment.name}</strong>
                    <span className="code-label">{environment.slug}</span>
                  </div>
                  <span>
                    {environment.current_revision_id
                      ? t("detail.currentRevision", {
                          revision: environment.current_revision_id,
                        })
                      : t("detail.noRevision")}
                  </span>
                </li>
              ))}
            </ul>
          </>
        )}
      </section>

      <nav
        className="project-tabs"
        role="tablist"
        aria-label={t("detail.tabList")}
      >
        {projectTabs.map((tab, index) => (
          <Link
            key={tab}
            id={`project-tab-${tab}`}
            role="tab"
            aria-controls={`project-panel-${tab}`}
            aria-selected={activeTab === tab}
            tabIndex={activeTab === tab ? 0 : -1}
            className={
              activeTab === tab ? "project-tab active-tab" : "project-tab"
            }
            to={{ search: `?${approvedSearch(selectedEnvironment, tab)}` }}
            onKeyDown={(event) => handleTabKeyDown(event, index)}
          >
            {t(`tabs.${tab}`)}
          </Link>
        ))}
      </nav>

      {projectTabs.map((tab) => (
        <section
          key={tab}
          id={`project-panel-${tab}`}
          className="project-tab-panel"
          role="tabpanel"
          aria-labelledby={`project-tab-${tab}`}
          hidden={activeTab !== tab}
        >
          {activeTab === tab && tab === "configuration" ? (
            <ConfigTable
              client={client}
              projectSlug={project.slug}
              environmentSlug={selectedEnvironmentRecord?.slug ?? ""}
              canWrite={canWrite}
              refreshEpoch={revisionRefreshEpoch}
              onRevisionChanged={handleRevisionChanged}
            />
          ) : null}
          {activeTab === tab && tab === "versions" ? (
            <VersionList
              client={client}
              projectSlug={project.slug}
              environmentSlug={selectedEnvironmentRecord?.slug ?? ""}
              canWrite={canWrite}
              refreshEpoch={revisionRefreshEpoch}
              onRevisionChanged={handleRevisionChanged}
            />
          ) : null}
          {activeTab === tab && tab === "members" ? (
            <ProjectMembers projectSlug={project.slug} canManage={canManage} />
          ) : null}
        </section>
      ))}

      {creatingEnvironment ? (
        <CreateEnvironmentDialog
          client={client}
          projectSlug={project.slug}
          onCancel={closeEnvironmentCreation}
          onCreated={(environment) => {
            addEnvironment(environment);
            closeEnvironmentCreation();
          }}
        />
      ) : null}
    </article>
  );
}

function CreateEnvironmentDialog({
  client,
  onCancel,
  onCreated,
  projectSlug,
}: {
  client: ReturnType<typeof useAuth>["client"];
  onCancel(): void;
  onCreated(environment: ProjectEnvironment): void;
  projectSlug: string;
}) {
  const { t } = useTranslation("projects");
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const submittingRef = useRef(false);
  const operationGenerationRef = useRef(0);
  const slugRef = useRef<HTMLInputElement>(null);
  const [formError, setFormError] = useState("");
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    return () => {
      operationGenerationRef.current += 1;
    };
  }, []);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (submittingRef.current) {
      return;
    }
    submittingRef.current = true;
    const operationGeneration = ++operationGenerationRef.current;
    setSubmitting(true);
    setFormError("");
    setFieldErrors({});
    try {
      const response = await client.post<CreateEnvironmentResponse>(
        `/projects/${encodeURIComponent(projectSlug)}/environments`,
        { slug, name },
      );
      if (operationGenerationRef.current === operationGeneration) {
        onCreated(response.environment);
      }
    } catch (error) {
      if (operationGenerationRef.current !== operationGeneration) {
        return;
      }
      if (error instanceof APIError && error.status === 422) {
        setFieldErrors(
          localizePresentFields(error.fields, {
            slug: "validation.environment.slug",
            name: "validation.environment.name",
          }),
        );
        setFormError("errors.checkFields");
      } else if (
        error instanceof APIError &&
        (error.status === 409 || error.code === "resource_conflict")
      ) {
        setFormError("errors.environmentConflict");
      } else {
        setFormError("errors.environmentUnavailableCreate");
      }
    } finally {
      if (operationGenerationRef.current === operationGeneration) {
        submittingRef.current = false;
        setSubmitting(false);
      }
    }
  }

  return (
    <ModalDialog
      labelledBy="new-environment-title"
      initialFocusRef={slugRef}
      closeDisabled={submitting}
      onRequestClose={onCancel}
    >
      <header className="dialog-heading">
        <div>
          <p className="section-index">{t("environmentForm.index")}</p>
          <h2 id="new-environment-title">{t("environmentForm.title")}</h2>
        </div>
        <button
          className="text-button"
          type="button"
          disabled={submitting}
          onClick={onCancel}
        >
          {t("common:actions.cancel")}
        </button>
      </header>
      <form
        noValidate
        className="resource-form"
        onSubmit={(event) => void handleSubmit(event)}
      >
        <div className="form-field">
          <label htmlFor="environment-slug">{t("environmentForm.slug")}</label>
          <input
            ref={slugRef}
            id="environment-slug"
            name="slug"
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            required
            value={slug}
            disabled={submitting}
            aria-invalid={fieldErrors.slug ? "true" : undefined}
            aria-describedby={
              fieldErrors.slug ? "environment-slug-error" : undefined
            }
            onChange={(event) => setSlug(event.currentTarget.value)}
          />
          {fieldErrors.slug ? (
            <p className="field-error" id="environment-slug-error">
              {t(fieldErrors.slug)}
            </p>
          ) : null}
        </div>
        <div className="form-field">
          <label htmlFor="environment-name">{t("environmentForm.name")}</label>
          <input
            id="environment-name"
            name="name"
            required
            value={name}
            disabled={submitting}
            aria-invalid={fieldErrors.name ? "true" : undefined}
            aria-describedby={
              fieldErrors.name ? "environment-name-error" : undefined
            }
            onChange={(event) => setName(event.currentTarget.value)}
          />
          {fieldErrors.name ? (
            <p className="field-error" id="environment-name-error">
              {t(fieldErrors.name)}
            </p>
          ) : null}
        </div>
        <div className="form-message" aria-live="polite">
          {formError ? <p role="alert">{t(formError)}</p> : null}
        </div>
        <button className="primary-button" type="submit" disabled={submitting}>
          {submitting
            ? t("environmentForm.submitting")
            : t("environmentForm.submit")}
        </button>
      </form>
    </ModalDialog>
  );
}

function safeTab(value: string | null): ProjectTab {
  return value === "versions" || value === "members" ? value : "configuration";
}

function approvedSearch(environment: string, tab: ProjectTab): URLSearchParams {
  const next = new URLSearchParams();
  if (environment) {
    next.set("environment", environment);
  }
  if (tab !== "configuration") {
    next.set("tab", tab);
  }
  return next;
}
