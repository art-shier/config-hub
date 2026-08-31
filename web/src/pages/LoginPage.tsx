import { useEffect, useRef, useState, type FormEvent } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { APIError } from "../api/client";
import { useAuth } from "../auth/AuthProvider";
import { LanguageSwitcher } from "../components/LanguageSwitcher";
import { useTranslation } from "react-i18next";

const knownDestinations = new Set([
  "/projects",
  "/machine-access",
  "/members",
  "/system",
]);
const projectDestinationPattern = /^\/projects\/[a-z0-9][a-z0-9-]{0,62}$/u;

interface LoginLocationState {
  from?: {
    pathname?: unknown;
    search?: unknown;
  };
}

export function LoginPage() {
  const { t } = useTranslation("auth");
  const { loading, login, user } = useAuth();
  const location = useLocation();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [passwordVisible, setPasswordVisible] = useState(false);
  const mountedRef = useRef(false);
  const destination = safeDestination(location.state);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  if (user !== null) {
    return <Navigate to={destination} replace />;
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (loading || submitting) {
      return;
    }

    setError("");
    setSubmitting(true);
    try {
      await login(username, password);
    } catch (caught) {
      if (mountedRef.current) {
        setError(t(loginErrorKey(caught)));
      }
    } finally {
      if (mountedRef.current) {
        setPassword("");
        setSubmitting(false);
      }
    }
  }

  return (
    <main className="login-page">
      <section className="login-introduction" aria-labelledby="login-title">
        <div className="login-brand">
          <span className="brand-mark" aria-hidden="true" />
          <span className="brand-name">ConfigHub</span>
        </div>
        <p className="eyebrow">{t("login.eyebrow")}</p>
        <h1 id="login-title">{t("login.title")}</h1>
        <p className="login-summary">
          {t("login.summary")}
        </p>
        <dl className="login-facts">
          <div>
            <dt>{t("login.facts.access")}</dt>
            <dd>{t("login.facts.accessValue")}</dd>
          </div>
          <div>
            <dt>{t("login.facts.session")}</dt>
            <dd>{t("login.facts.sessionValue")}</dd>
          </div>
        </dl>
      </section>

      <section className="login-form-region" aria-label={t("login.region")}>
        <div className="login-form-heading">
          <p className="section-index">{t("login.sectionIndex")}</p>
          <h2>{t("login.credentialsTitle")}</h2>
          <p>{t("login.credentialsDescription")}</p>
        </div>
        <form
          className="login-form"
          noValidate
          onSubmit={(event) => void handleSubmit(event)}
        >
          <LanguageSwitcher className="login-language-switcher" />
          <label htmlFor="username">{t("login.fields.username")}</label>
          <input
            id="username"
            name="username"
            type="text"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            required
            value={username}
            disabled={submitting}
            onChange={(event) => setUsername(event.currentTarget.value)}
          />

          <label htmlFor="password">{t("login.fields.password")}</label>
          <div className="password-field">
            <input
              id="password"
              name="password"
              type={passwordVisible ? "text" : "password"}
              autoComplete="current-password"
              required
              value={password}
              disabled={submitting}
              onChange={(event) => setPassword(event.currentTarget.value)}
            />
            <button
              className="text-button password-visibility-button"
              type="button"
              aria-pressed={passwordVisible}
              disabled={submitting}
              onClick={() => setPasswordVisible((visible) => !visible)}
            >
              {t(
                passwordVisible
                  ? "login.passwordVisibility.hide"
                  : "login.passwordVisibility.show",
              )}
            </button>
          </div>

          <div className="form-message" aria-live="polite" aria-atomic="true">
            {error ? <p role="alert">{error}</p> : null}
          </div>

          <button
            className="primary-button"
            type="submit"
            disabled={loading || submitting}
          >
            {submitting ? t("login.pending") : t("login.action")}
          </button>
          {loading ? (
            <p className="session-check" role="status">
              {t("login.sessionCheck")}
            </p>
          ) : null}
        </form>
      </section>
    </main>
  );
}

function safeDestination(state: unknown): string {
  const from = (state as LoginLocationState | null)?.from;
  if (
    from === undefined ||
    typeof from.pathname !== "string" ||
    (!knownDestinations.has(from.pathname) &&
      !projectDestinationPattern.test(from.pathname))
  ) {
    return "/projects";
  }
  const search =
    typeof from.search === "string" &&
    (from.search === "" ||
      (from.search.startsWith("?") && !from.search.includes("#")))
      ? from.search
      : "";
  return `${from.pathname}${search}`;
}

function loginErrorKey(error: unknown):
  | "login.errors.invalidCredentials"
  | "login.errors.rateLimited"
  | "login.errors.network" {
  if (error instanceof APIError) {
    if (error.status === 401 || error.code === "invalid_credentials") {
      return "login.errors.invalidCredentials";
    }
    if (error.status === 429 || error.code === "rate_limited") {
      return "login.errors.rateLimited";
    }
  }
  return "login.errors.network";
}
