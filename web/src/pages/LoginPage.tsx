import { useEffect, useRef, useState, type FormEvent } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { APIError } from "../api/client";
import { useAuth } from "../auth/AuthProvider";

const knownDestinations = new Set([
  "/projects",
  "/machine-access",
  "/members",
  "/system",
]);

interface LoginLocationState {
  from?: {
    pathname?: unknown;
    search?: unknown;
  };
}

export function LoginPage() {
  const { loading, login, user } = useAuth();
  const location = useLocation();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
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
        setError(loginErrorMessage(caught));
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
        <p className="eyebrow">Internal configuration control</p>
        <h1 id="login-title">Sign in to the team ledger.</h1>
        <p className="login-summary">
          Review current values, trace revisions, and keep machine access
          scoped to the work that needs it.
        </p>
        <dl className="login-facts">
          <div>
            <dt>Access</dt>
            <dd>Team accounts only</dd>
          </div>
          <div>
            <dt>Session</dt>
            <dd>Managed by ConfigHub</dd>
          </div>
        </dl>
      </section>

      <section className="login-form-region" aria-label="Account sign in">
        <div className="login-form-heading">
          <p className="section-index">Session / 01</p>
          <h2>Account credentials</h2>
          <p>Use the username and password issued by your administrator.</p>
        </div>
        <form className="login-form" onSubmit={(event) => void handleSubmit(event)}>
          <label htmlFor="username">Username</label>
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

          <label htmlFor="password">Password</label>
          <input
            id="password"
            name="password"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            disabled={submitting}
            onChange={(event) => setPassword(event.currentTarget.value)}
          />

          <div className="form-message" aria-live="polite" aria-atomic="true">
            {error ? <p role="alert">{error}</p> : null}
          </div>

          <button
            className="primary-button"
            type="submit"
            disabled={loading || submitting}
          >
            {submitting ? "Signing in…" : "Sign in"}
          </button>
          {loading ? (
            <p className="session-check" role="status">
              Checking existing session…
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
    !knownDestinations.has(from.pathname)
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

function loginErrorMessage(error: unknown): string {
  if (error instanceof APIError) {
    if (error.status === 401 || error.code === "invalid_credentials") {
      return "Username or password wasn’t recognized.";
    }
    if (error.status === 429 || error.code === "rate_limited") {
      return "Too many sign-in attempts. Wait a moment and try again.";
    }
  }
  return "ConfigHub couldn’t be reached. Check the server and try again.";
}
