import { useEffect, useRef, useState } from "react";
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
  const [navigationOpen, setNavigationOpen] = useState(false);
  const navigationButtonRef = useRef<HTMLButtonElement>(null);
  const navigationRef = useRef<HTMLElement>(null);

  useEffect(() => {
    if (!navigationOpen) {
      return;
    }
    navigationRef.current?.querySelector<HTMLAnchorElement>("a")?.focus();

    function handlePointerDown(event: PointerEvent) {
      const target = event.target;
      if (!(target instanceof Node)) {
        return;
      }
      if (!navigationRef.current?.contains(target) && !navigationButtonRef.current?.contains(target)) {
        setNavigationOpen(false);
      }
    }

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key !== "Escape") {
        return;
      }
      event.preventDefault();
      setNavigationOpen(false);
      navigationButtonRef.current?.focus();
    }

    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [navigationOpen]);

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
        <button
          ref={navigationButtonRef}
          className="navigation-button"
          type="button"
          aria-controls="primary-navigation"
          aria-expanded={navigationOpen}
          aria-label={navigationOpen ? "Close navigation" : "Open navigation"}
          onClick={() => setNavigationOpen((current) => !current)}
        >
          <span aria-hidden="true">Menu</span>
        </button>
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
        <nav
          ref={navigationRef}
          id="primary-navigation"
          className={navigationOpen ? "primary-nav primary-nav-open" : "primary-nav"}
          aria-label="Primary"
        >
          <p className="nav-label">Workspace</p>
          <ul>
            {navigation.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  onClick={() => setNavigationOpen(false)}
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
