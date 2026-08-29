import { useState } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { useAuth } from "../auth/AuthProvider";

const memberNavigation = [{ to: "/projects", label: "Projects" }];
const adminNavigation = [
  { to: "/machine-access", label: "Machine Access" },
  { to: "/members", label: "Members" },
  { to: "/system", label: "System" },
];
const LOGOUT_ERROR_MESSAGE =
  "ConfigHub couldn’t confirm sign-out. You’re still signed in. Check the server and try again.";

export function AppShell() {
  const { logout, user } = useAuth();
  const [loggingOut, setLoggingOut] = useState(false);
  const [logoutError, setLogoutError] = useState("");

  if (user === null) {
    return null;
  }

  const navigation =
    user.role === "admin"
      ? [...memberNavigation, ...adminNavigation]
      : memberNavigation;

  async function handleLogout() {
    if (loggingOut) {
      return;
    }
    setLogoutError("");
    setLoggingOut(true);
    try {
      await logout();
    } catch {
      setLogoutError(LOGOUT_ERROR_MESSAGE);
    } finally {
      setLoggingOut(false);
    }
  }

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      <header className="app-header">
        <div className="brand-lockup">
          <span className="brand-mark" aria-hidden="true" />
          <div>
            <span className="brand-name">ConfigHub</span>
            <span className="brand-context">Control ledger</span>
          </div>
        </div>
        <div className="session-summary">
          <span className="session-user">{user.display_name}</span>
          <span className="session-role">{user.role}</span>
          <button
            className="quiet-button"
            type="button"
            disabled={loggingOut}
            onClick={() => void handleLogout()}
          >
            {loggingOut ? "Signing out…" : "Sign out"}
          </button>
          {logoutError ? (
            <p
              className="logout-error"
              role="status"
              aria-live="polite"
              aria-atomic="true"
            >
              {logoutError}
            </p>
          ) : null}
        </div>
      </header>
      <div className="app-frame">
        <nav className="primary-nav" aria-label="Primary">
          <p className="nav-label">Workspace</p>
          <ul>
            {navigation.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  className={({ isActive }) =>
                    isActive ? "nav-link nav-link-active" : "nav-link"
                  }
                >
                  {item.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <main id="main-content" className="app-content" tabIndex={-1}>
          <Outlet />
        </main>
      </div>
    </div>
  );
}
