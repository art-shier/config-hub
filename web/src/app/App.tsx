import {
  BrowserRouter,
  Navigate,
  Outlet,
  Route,
  Routes,
  useLocation,
} from "react-router-dom";
import { AuthProvider, useAuth } from "../auth/AuthProvider";
import { LoginPage } from "../pages/LoginPage";
import { ProjectPage } from "../pages/ProjectPage";
import { ProjectsPage } from "../pages/ProjectsPage";
import { AppShell } from "./AppShell";

export function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route element={<RequireSession />}>
            <Route element={<AppShell />}>
              <Route index element={<Navigate to="/projects" replace />} />
              <Route path="/projects" element={<ProjectsPage />} />
              <Route path="/projects/:project" element={<ProjectPage />} />
              <Route element={<RequireAdmin />}>
                <Route
                  path="/machine-access"
                  element={
                    <PlaceholderPage
                      eyebrow="Scoped credentials"
                      title="Machine Access"
                      description="Machine identities and token controls will appear here."
                    />
                  }
                />
                <Route
                  path="/members"
                  element={
                    <PlaceholderPage
                      eyebrow="Team permissions"
                      title="Members"
                      description="Project membership controls will appear here."
                    />
                  }
                />
                <Route
                  path="/system"
                  element={
                    <PlaceholderPage
                      eyebrow="Service status"
                      title="System"
                      description="Operational status will appear here."
                    />
                  }
                />
              </Route>
              <Route
                path="*"
                element={
                  <PlaceholderPage
                    eyebrow="Navigation"
                    title="Page not found"
                    description="This address does not match a ConfigHub page."
                  />
                }
              />
            </Route>
          </Route>
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  );
}

function RequireSession() {
  const { loading, user } = useAuth();
  const location = useLocation();

  if (loading) {
    return <SessionLoading />;
  }
  if (user === null) {
    return (
      <Navigate
        to="/login"
        replace
        state={{
          from: { pathname: location.pathname, search: location.search },
        }}
      />
    );
  }
  return <Outlet />;
}

function RequireAdmin() {
  const { user } = useAuth();
  return user?.role === "admin" ? <Outlet /> : <Navigate to="/projects" replace />;
}

function SessionLoading() {
  return (
    <main className="session-loading" aria-labelledby="session-loading-title">
      <span className="brand-mark" aria-hidden="true" />
      <p className="eyebrow">ConfigHub session</p>
      <h1 id="session-loading-title">Checking access…</h1>
    </main>
  );
}

function PlaceholderPage({
  description,
  eyebrow,
  title,
}: {
  description: string;
  eyebrow: string;
  title: string;
}) {
  return (
    <section className="page-heading">
      <p className="eyebrow">{eyebrow}</p>
      <h1>{title}</h1>
      <p>{description}</p>
    </section>
  );
}
