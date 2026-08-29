import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { APIError } from "../api/client";
import type {
  Environment as ProjectEnvironment,
  ProjectDetail,
} from "../api/types";
import { useAuth } from "../auth/AuthProvider";
import { ProjectMembers } from "../features/members/ProjectMembers";

interface ProjectDetailResponse {
  project: ProjectDetail;
}

interface CreateEnvironmentResponse {
  environment: ProjectEnvironment;
}

type ProjectTab = "configuration" | "versions" | "members";
type LoadFailure = "not-found" | "unavailable" | null;

const projectSlugPattern = /^[a-z0-9][a-z0-9-]{0,62}$/u;
const projectTabs: Array<{ id: ProjectTab; label: string }> = [
  { id: "configuration", label: "Configuration" },
  { id: "versions", label: "Versions" },
  { id: "members", label: "Members" },
];

export function ProjectPage() {
  const { client, user } = useAuth();
  const { project: projectSlug = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const [project, setProject] = useState<ProjectDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [failure, setFailure] = useState<LoadFailure>(null);
  const [creatingEnvironment, setCreatingEnvironment] = useState(false);
  const newEnvironmentButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    let current = true;
    setProject(null);
    setFailure(null);
    if (!projectSlugPattern.test(projectSlug)) {
      setLoading(false);
      setFailure("not-found");
      return () => {
        current = false;
      };
    }
    setLoading(true);
    void client
      .get<ProjectDetailResponse>(`/projects/${encodeURIComponent(projectSlug)}`)
      .then((response) => {
        if (current) {
          setProject(response.project);
        }
      })
      .catch((error) => {
        if (current) {
          setFailure(
            error instanceof APIError && error.status === 404
              ? "not-found"
              : "unavailable",
          );
        }
      })
      .finally(() => {
        if (current) {
          setLoading(false);
        }
      });
    return () => {
      current = false;
    };
  }, [client, projectSlug]);

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
    window.requestAnimationFrame(() => newEnvironmentButtonRef.current?.focus());
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

  if (loading) {
    return (
      <section className="resource-page">
        <p className="loading-line" role="status">
          Loading project…
        </p>
      </section>
    );
  }
  if (failure !== null || project === null) {
    const notFound = failure === "not-found";
    return (
      <section className="resource-page page-error-state">
        <p className="eyebrow">Project register</p>
        <h1>{notFound ? "Project not found" : "Project unavailable"}</h1>
        <p>
          {notFound
            ? "This project address does not match an available project."
            : "The project couldn’t be loaded. Check the server and try again."}
        </p>
        <Link className="text-link" to="/projects">
          Return to projects
        </Link>
      </section>
    );
  }

  const canManage = user?.role === "admin" && project.permission === "admin";
  const selectedEnvironmentRecord = project.environments.find(
    (environment) => environment.slug === selectedEnvironment,
  );

  return (
    <article className="resource-page project-workspace" aria-labelledby="project-title">
      <Link className="back-link" to="/projects">
        ← Project register
      </Link>
      <header className="project-heading">
        <div>
          <p className="eyebrow">Project / {project.slug}</p>
          <h1 id="project-title">{project.name}</h1>
          <p>{project.description || "No project description provided."}</p>
        </div>
        <dl className="project-facts">
          <div>
            <dt>Slug</dt>
            <dd className="code-label">{project.slug}</dd>
          </div>
          <div>
            <dt>Access</dt>
            <dd>{titleCase(project.permission)} access</dd>
          </div>
          <div>
            <dt>Updated</dt>
            <dd>{formatDate(project.updated_at)}</dd>
          </div>
        </dl>
      </header>

      <section className="environment-section" aria-labelledby="environments-title">
        <header className="section-heading">
          <div>
            <p className="section-index">Environment register</p>
            <h2 id="environments-title">Environments</h2>
            <p>Select the environment carried through every project section.</p>
          </div>
          {canManage ? (
            <button
              ref={newEnvironmentButtonRef}
              className="primary-button action-button"
              type="button"
              onClick={() => setCreatingEnvironment(true)}
            >
              New environment
            </button>
          ) : null}
        </header>

        {project.environments.length === 0 ? (
          <div className="empty-state compact-empty">
            <h3>No environments yet</h3>
            <p>
              {canManage
                ? "Create an environment before publishing configuration."
                : "A project administrator has not created an environment yet."}
            </p>
          </div>
        ) : (
          <>
            <div className="environment-picker">
              <label htmlFor="active-environment">Active environment</label>
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
            <ul className="environment-list" aria-label="Project environments">
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
                      ? `Current revision ${environment.current_revision_id}`
                      : "No revision published"}
                  </span>
                </li>
              ))}
            </ul>
          </>
        )}
      </section>

      <nav className="project-tabs" role="tablist" aria-label="Project sections">
        {projectTabs.map((tab) => (
          <Link
            key={tab.id}
            id={`project-tab-${tab.id}`}
            role="tab"
            aria-controls={`project-panel-${tab.id}`}
            aria-selected={activeTab === tab.id}
            className={activeTab === tab.id ? "project-tab active-tab" : "project-tab"}
            to={{ search: `?${approvedSearch(selectedEnvironment, tab.id)}` }}
          >
            {tab.label}
          </Link>
        ))}
      </nav>

      <section
        id={`project-panel-${activeTab}`}
        className="project-tab-panel"
        role="tabpanel"
        aria-labelledby={`project-tab-${activeTab}`}
      >
        {activeTab === "configuration" ? (
          <TaskPlaceholder
            title="Configuration"
            detail={
              selectedEnvironmentRecord
                ? `Configuration editing for ${selectedEnvironmentRecord.name} arrives in Task 14.`
                : "Create an environment before editing configuration in Task 14."
            }
          />
        ) : null}
        {activeTab === "versions" ? (
          <TaskPlaceholder
            title="Versions"
            detail={
              selectedEnvironmentRecord
                ? `Version history arrives in Task 14 for ${selectedEnvironmentRecord.name}.`
                : "Version history arrives in Task 14 after an environment is created."
            }
          />
        ) : null}
        {activeTab === "members" ? (
          <ProjectMembers projectSlug={project.slug} canManage={canManage} />
        ) : null}
      </section>

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
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const submittingRef = useRef(false);
  const mountedRef = useRef(true);
  const slugRef = useRef<HTMLInputElement>(null);
  const [formError, setFormError] = useState("");
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    slugRef.current?.focus();
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        onCancel();
      }
    }
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      mountedRef.current = false;
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [onCancel]);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (submittingRef.current) {
      return;
    }
    submittingRef.current = true;
    setSubmitting(true);
    setFormError("");
    setFieldErrors({});
    try {
      const response = await client.post<CreateEnvironmentResponse>(
        `/projects/${encodeURIComponent(projectSlug)}/environments`,
        { slug, name },
      );
      if (mountedRef.current) {
        onCreated(response.environment);
      }
    } catch (error) {
      if (!mountedRef.current) {
        return;
      }
      if (error instanceof APIError && error.status === 422) {
        setFieldErrors(error.fields);
        setFormError("Check the marked fields and try again.");
      } else if (
        error instanceof APIError &&
        (error.status === 409 || error.code === "resource_conflict")
      ) {
        setFormError(
          "That environment slug is already in use. Choose another slug.",
        );
      } else {
        setFormError(
          "The environment couldn’t be created. Keep this draft and try again.",
        );
      }
    } finally {
      if (mountedRef.current) {
        submittingRef.current = false;
        setSubmitting(false);
      }
    }
  }

  return (
    <div className="dialog-backdrop">
      <section
        className="dialog-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="new-environment-title"
      >
        <header className="dialog-heading">
          <div>
            <p className="section-index">Environment register / New</p>
            <h2 id="new-environment-title">New environment</h2>
          </div>
          <button className="text-button" type="button" onClick={onCancel}>
            Cancel
          </button>
        </header>
        <form className="resource-form" onSubmit={(event) => void handleSubmit(event)}>
          <div className="form-field">
            <label htmlFor="environment-slug">Environment slug</label>
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
                {fieldErrors.slug}
              </p>
            ) : null}
          </div>
          <div className="form-field">
            <label htmlFor="environment-name">Environment name</label>
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
                {fieldErrors.name}
              </p>
            ) : null}
          </div>
          <div className="form-message" aria-live="polite">
            {formError ? <p role="alert">{formError}</p> : null}
          </div>
          <button className="primary-button" type="submit" disabled={submitting}>
            {submitting ? "Creating environment…" : "Create environment"}
          </button>
        </form>
      </section>
    </div>
  );
}

function TaskPlaceholder({ detail, title }: { detail: string; title: string }) {
  return (
    <div className="task-placeholder">
      <p className="section-index">Selected environment</p>
      <h2>{title}</h2>
      <p>{detail}</p>
    </div>
  );
}

function safeTab(value: string | null): ProjectTab {
  return value === "versions" || value === "members"
    ? value
    : "configuration";
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

function titleCase(value: string): string {
  return `${value.charAt(0).toUpperCase()}${value.slice(1)}`;
}

function formatDate(value: string): string {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) {
    return "Time unavailable";
  }
  try {
    return new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(
      date,
    );
  } catch {
    return date.toISOString().slice(0, 10);
  }
}
