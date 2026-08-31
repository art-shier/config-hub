import { useEffect, useRef, useState } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthProvider";
import { LanguageSwitcher } from "../components/LanguageSwitcher";

const memberNavigation = [{ to: "/projects", key: "projects" }];
const adminNavigation = [
  { to: "/machine-access", key: "machineAccess" },
  { to: "/members", key: "members" },
  { to: "/system", key: "system" },
];
type LogoutErrorKey = "auth:signOut.failure";

export function AppShell() {
  const { t } = useTranslation(["common", "auth"]);
  const { logout, user } = useAuth();
  const [loggingOut, setLoggingOut] = useState(false);
  const [logoutErrorKey, setLogoutErrorKey] = useState<LogoutErrorKey | null>(
    null,
  );
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
    setLogoutErrorKey(null);
    setLoggingOut(true);
    try {
      await logout();
    } catch {
      setLogoutErrorKey("auth:signOut.failure");
    } finally {
      setLoggingOut(false);
    }
  }

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        {t("skipLink")}
      </a>
      <header className="app-header">
        <div className="brand-lockup">
          <span className="brand-mark" aria-hidden="true" />
          <div>
            <span className="brand-name">ConfigHub</span>
            <span className="brand-context">{t("brandContext")}</span>
          </div>
        </div>
        <button
          ref={navigationButtonRef}
          className="navigation-button"
          type="button"
          aria-controls="primary-navigation"
          aria-expanded={navigationOpen}
          aria-label={t(
            navigationOpen ? "navigation.close" : "navigation.open",
          )}
          onClick={() => setNavigationOpen((current) => !current)}
        >
          <span aria-hidden="true">{t("navigation.menu")}</span>
        </button>
        <div className="session-summary">
          <span className="session-user">{user.display_name}</span>
          <span className="session-role">{t(`roles.${user.role}`)}</span>
          <LanguageSwitcher />
          <button
            className="quiet-button"
            type="button"
            disabled={loggingOut}
            onClick={() => void handleLogout()}
          >
            {loggingOut
              ? t("auth:signOut.pending")
              : t("auth:signOut.action")}
          </button>
          {logoutErrorKey ? (
            <p
              className="logout-error"
              role="status"
              aria-live="polite"
              aria-atomic="true"
            >
              {t(logoutErrorKey)}
            </p>
          ) : null}
        </div>
      </header>
      <div className="app-frame">
        <nav
          ref={navigationRef}
          id="primary-navigation"
          className={navigationOpen ? "primary-nav primary-nav-open" : "primary-nav"}
          aria-label={t("navigation.primary")}
        >
          <p className="nav-label">{t("navigation.workspace")}</p>
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
                  {t(`navigation.${item.key}`)}
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
