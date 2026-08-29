import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { Link } from "react-router-dom";
import { APIError } from "../api/client";
import type { Project } from "../api/types";
import { useAuth } from "../auth/AuthProvider";

interface ProjectListResponse {
  projects: Project[];
}

interface CreateProjectResponse {
  project: Project;
}

const projectSlugPattern = /^[a-z0-9][a-z0-9-]{0,62}$/u;

export function ProjectsPage() {
  const { client, user } = useAuth();
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [creating, setCreating] = useState(false);
  const newProjectButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    let current = true;
    setLoading(true);
    setLoadError("");
    void client
      .get<ProjectListResponse>("/projects")
      .then((response) => {
        if (current) {
          setProjects(response.projects);
        }
      })
      .catch(() => {
        if (current) {
          setLoadError(
            "Projects couldn’t be loaded. Check the server and try again.",
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
  }, [client]);

  const closeCreation = useCallback(() => {
    setCreating(false);
    window.requestAnimationFrame(() => newProjectButtonRef.current?.focus());
  }, []);

  function addProject(project: Project) {
    setProjects((current) =>
      [...current.filter((item) => item.id !== project.id && item.slug !== project.slug), project].sort(
        (left, right) => left.name.localeCompare(right.name),
      ),
    );
  }

  return (
    <section className="resource-page" aria-labelledby="projects-title">
      <header className="resource-heading">
        <div>
          <p className="eyebrow">Configuration inventory</p>
          <h1 id="projects-title">Projects</h1>
          <p>
            Open a project to review its environments, revisions, and access.
          </p>
        </div>
        {user?.role === "admin" ? (
          <button
            ref={newProjectButtonRef}
            className="primary-button action-button"
            type="button"
            onClick={() => setCreating(true)}
          >
            New project
          </button>
        ) : null}
      </header>

      {loading ? (
        <p className="loading-line" role="status">
          Loading projects…
        </p>
      ) : null}
      {loadError ? (
        <div
          className="empty-state"
          aria-live="polite"
          aria-atomic="true"
        >
          <h2>Projects unavailable</h2>
          <p>{loadError}</p>
        </div>
      ) : null}
      {!loading && !loadError && projects.length === 0 ? (
        <div className="empty-state">
          <p className="section-index">Project register / Empty</p>
          <h2>No projects yet</h2>
          <p>
            {user?.role === "admin"
              ? "Create the first project to establish an environment ledger."
              : "A project administrator can grant access when a workspace is ready."}
          </p>
        </div>
      ) : null}
      {!loading && !loadError && projects.length > 0 ? (
        <div className="resource-register" aria-label="Visible projects">
          <div className="register-header" aria-hidden="true">
            <span>Project</span>
            <span>Description</span>
            <span>Last update</span>
          </div>
          <ul className="project-list">
            {projects.map((project) => (
              <li key={project.id}>
                <div className="project-identity">
                  <Link to={safeProjectPath(project.slug)}>{project.name}</Link>
                  <span className="code-label">{project.slug}</span>
                </div>
                <p className="project-description">
                  {project.description || "No description provided."}
                </p>
                <time dateTime={project.updated_at}>
                  Updated {formatUpdatedAt(project.updated_at)}
                </time>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {creating ? (
        <CreateProjectDialog
          client={client}
          onCancel={closeCreation}
          onCreated={(project) => {
            addProject(project);
            closeCreation();
          }}
        />
      ) : null}
    </section>
  );
}

function CreateProjectDialog({
  client,
  onCancel,
  onCreated,
}: {
  client: ReturnType<typeof useAuth>["client"];
  onCancel(): void;
  onCreated(project: Project): void;
}) {
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState("");
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const slugRef = useRef<HTMLInputElement>(null);
  const mountedRef = useRef(true);
  const submittingRef = useRef(false);

  useEffect(() => {
    slugRef.current?.focus();
    function closeOnEscape(event: globalThis.KeyboardEvent) {
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
      const response = await client.post<CreateProjectResponse>("/projects", {
        slug,
        name,
        description,
      });
      if (mountedRef.current) {
        onCreated(response.project);
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
        setFormError("That project slug is already in use. Choose another slug.");
      } else {
        setFormError(
          "The project couldn’t be created. Keep this draft and try again.",
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
        aria-labelledby="new-project-title"
      >
        <header className="dialog-heading">
          <div>
            <p className="section-index">Project register / New</p>
            <h2 id="new-project-title">New project</h2>
          </div>
          <button className="text-button" type="button" onClick={onCancel}>
            Cancel
          </button>
        </header>
        <form className="resource-form" onSubmit={(event) => void handleSubmit(event)}>
          <Field
            id="project-slug"
            label="Project slug"
            error={fieldErrors.slug}
          >
            <input
              ref={slugRef}
              id="project-slug"
              name="slug"
              autoCapitalize="none"
              autoComplete="off"
              spellCheck={false}
              required
              disabled={submitting}
              value={slug}
              aria-invalid={fieldErrors.slug ? "true" : undefined}
              aria-describedby={fieldErrors.slug ? "project-slug-error" : undefined}
              onChange={(event) => setSlug(event.currentTarget.value)}
            />
          </Field>
          <Field id="project-name" label="Project name" error={fieldErrors.name}>
            <input
              id="project-name"
              name="name"
              required
              disabled={submitting}
              value={name}
              aria-invalid={fieldErrors.name ? "true" : undefined}
              aria-describedby={fieldErrors.name ? "project-name-error" : undefined}
              onChange={(event) => setName(event.currentTarget.value)}
            />
          </Field>
          <Field
            id="project-description"
            label="Description"
            error={fieldErrors.description}
          >
            <textarea
              id="project-description"
              name="description"
              rows={4}
              disabled={submitting}
              value={description}
              aria-invalid={fieldErrors.description ? "true" : undefined}
              aria-describedby={
                fieldErrors.description ? "project-description-error" : undefined
              }
              onChange={(event) => setDescription(event.currentTarget.value)}
            />
          </Field>
          <div className="form-message" aria-live="polite" aria-atomic="true">
            {formError ? <p role="alert">{formError}</p> : null}
          </div>
          <button className="primary-button" type="submit" disabled={submitting}>
            {submitting ? "Creating project…" : "Create project"}
          </button>
        </form>
      </section>
    </div>
  );
}

function Field({
  children,
  error,
  id,
  label,
}: {
  children: React.ReactNode;
  error?: string;
  id: string;
  label: string;
}) {
  return (
    <div className="form-field">
      <label htmlFor={id}>{label}</label>
      {children}
      {error ? (
        <p className="field-error" id={`${id}-error`}>
          {error}
        </p>
      ) : null}
    </div>
  );
}

function safeProjectPath(slug: string): string {
  return projectSlugPattern.test(slug)
    ? `/projects/${encodeURIComponent(slug)}`
    : "/projects";
}

function formatUpdatedAt(value: string): string {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) {
    return "time unavailable";
  }
  try {
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: "medium",
      timeStyle: "short",
    }).format(date);
  } catch {
    return date.toISOString().replace("T", " ").replace(".000Z", " UTC");
  }
}
