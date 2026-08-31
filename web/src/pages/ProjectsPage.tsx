import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { APIError } from "../api/client";
import type { Project } from "../api/types";
import { useAuth } from "../auth/AuthProvider";
import { ModalDialog } from "../components/ModalDialog";
import { localizePresentFields } from "../i18n/apiErrors";
import { formatDate } from "../i18n/format";
import type { SupportedLocale } from "../i18n/locales";

interface ProjectListResponse {
  projects: Project[];
}

interface CreateProjectResponse {
  project: Project;
}

const projectSlugPattern = /^[a-z0-9][a-z0-9-]{0,62}$/u;

export function ProjectsPage() {
  const { client, user } = useAuth();
  const { i18n, t } = useTranslation("projects");
  const locale: SupportedLocale =
    i18n.resolvedLanguage === "zh-CN" ? "zh-CN" : "en-US";
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<"" | "errors.projectsUnavailable">(
    "",
  );
  const [loadAnnouncement, setLoadAnnouncement] = useState<
    "loadingProjects" | "projectsLoaded" | "projectsUnavailable"
  >("loadingProjects");
  const [creating, setCreating] = useState(false);
  const newProjectButtonRef = useRef<HTMLButtonElement>(null);
  const loadGenerationRef = useRef(0);

  const loadProjects = useCallback(async () => {
    const generation = ++loadGenerationRef.current;
    setLoading(true);
    setLoadError("");
    setLoadAnnouncement("loadingProjects");
    try {
      const response = await client.get<ProjectListResponse>("/projects");
      if (loadGenerationRef.current === generation) {
        setProjects(response.projects);
        setLoadAnnouncement("projectsLoaded");
      }
    } catch {
      if (loadGenerationRef.current === generation) {
        setLoadError("errors.projectsUnavailable");
        setLoadAnnouncement("projectsUnavailable");
      }
    } finally {
      if (loadGenerationRef.current === generation) {
        setLoading(false);
      }
    }
  }, [client]);

  useEffect(() => {
    void loadProjects();
    return () => {
      loadGenerationRef.current += 1;
    };
  }, [loadProjects]);

  const closeCreation = useCallback(() => {
    setCreating(false);
    window.requestAnimationFrame(() => newProjectButtonRef.current?.focus());
  }, []);

  function addProject(project: Project) {
    setProjects((current) =>
      [
        ...current.filter(
          (item) => item.id !== project.id && item.slug !== project.slug,
        ),
        project,
      ].sort((left, right) => left.name.localeCompare(right.name)),
    );
  }

  return (
    <section className="resource-page" aria-labelledby="projects-title">
      <header className="resource-heading">
        <div>
          <p className="eyebrow">{t("list.eyebrow")}</p>
          <h1 id="projects-title">{t("list.title")}</h1>
          <p>{t("list.summary")}</p>
        </div>
        {user?.role === "admin" ? (
          <button
            ref={newProjectButtonRef}
            className="primary-button action-button"
            type="button"
            onClick={() => setCreating(true)}
          >
            {t("list.create")}
          </button>
        ) : null}
      </header>

      {loading ? (
        <p className="loading-line" role="status">
          {t(`states.${loadAnnouncement}`)}
        </p>
      ) : (
        <div className="sr-status" aria-live="polite" aria-atomic="true">
          {t(`states.${loadAnnouncement}`)}
        </div>
      )}
      {loadError ? (
        <div className="empty-state" aria-live="polite" aria-atomic="true">
          <h2>{t("states.projectsUnavailable")}</h2>
          <p>{t(loadError)}</p>
          <button
            className="secondary-button"
            type="button"
            onClick={() => void loadProjects()}
          >
            {t("actions.retry")}
          </button>
        </div>
      ) : null}
      {!loading && !loadError && projects.length === 0 ? (
        <div className="empty-state">
          <p className="section-index">{t("empty.listIndex")}</p>
          <h2>{t("empty.listTitle")}</h2>
          <p>
            {user?.role === "admin"
              ? t("empty.adminList")
              : t("empty.memberList")}
          </p>
        </div>
      ) : null}
      {!loading && !loadError && projects.length > 0 ? (
        <div className="resource-register" aria-label={t("list.register")}>
          <div className="register-header" aria-hidden="true">
            <span>{t("list.project")}</span>
            <span>{t("list.description")}</span>
            <span>{t("list.lastUpdate")}</span>
          </div>
          <ul className="project-list">
            {projects.map((project) => (
              <li key={project.id}>
                <div className="project-identity">
                  <Link to={safeProjectPath(project.slug)}>{project.name}</Link>
                  <span className="code-label">{project.slug}</span>
                </div>
                <p className="project-description">
                  {project.description || t("list.noDescription")}
                </p>
                <time dateTime={project.updated_at}>
                  {t("list.updated", {
                    date: formatDate(
                      project.updated_at,
                      locale,
                      t("states.unavailable"),
                    ),
                  })}
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
  const { t } = useTranslation("projects");
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState("");
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const slugRef = useRef<HTMLInputElement>(null);
  const operationGenerationRef = useRef(0);
  const submittingRef = useRef(false);

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
      const response = await client.post<CreateProjectResponse>("/projects", {
        slug,
        name,
        description,
      });
      if (operationGenerationRef.current === operationGeneration) {
        onCreated(response.project);
      }
    } catch (error) {
      if (operationGenerationRef.current !== operationGeneration) {
        return;
      }
      if (error instanceof APIError && error.status === 422) {
        setFieldErrors(
          localizePresentFields(error.fields, {
            slug: "validation.project.slug",
            name: "validation.project.name",
            description: "validation.project.description",
          }),
        );
        setFormError("errors.checkFields");
      } else if (
        error instanceof APIError &&
        (error.status === 409 || error.code === "resource_conflict")
      ) {
        setFormError("errors.projectConflict");
      } else {
        setFormError("errors.projectUnavailableCreate");
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
      labelledBy="new-project-title"
      initialFocusRef={slugRef}
      closeDisabled={submitting}
      onRequestClose={onCancel}
    >
      <header className="dialog-heading">
        <div>
          <p className="section-index">{t("projectForm.index")}</p>
          <h2 id="new-project-title">{t("projectForm.title")}</h2>
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
        <Field
          id="project-slug"
          label={t("projectForm.slug")}
          error={fieldErrors.slug ? t(fieldErrors.slug) : undefined}
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
            aria-describedby={
              fieldErrors.slug ? "project-slug-error" : undefined
            }
            onChange={(event) => setSlug(event.currentTarget.value)}
          />
        </Field>
        <Field
          id="project-name"
          label={t("projectForm.name")}
          error={fieldErrors.name ? t(fieldErrors.name) : undefined}
        >
          <input
            id="project-name"
            name="name"
            required
            disabled={submitting}
            value={name}
            aria-invalid={fieldErrors.name ? "true" : undefined}
            aria-describedby={
              fieldErrors.name ? "project-name-error" : undefined
            }
            onChange={(event) => setName(event.currentTarget.value)}
          />
        </Field>
        <Field
          id="project-description"
          label={t("projectForm.description")}
          error={
            fieldErrors.description ? t(fieldErrors.description) : undefined
          }
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
          {formError ? <p role="alert">{t(formError)}</p> : null}
        </div>
        <button className="primary-button" type="submit" disabled={submitting}>
          {submitting ? t("projectForm.submitting") : t("projectForm.submit")}
        </button>
      </form>
    </ModalDialog>
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
