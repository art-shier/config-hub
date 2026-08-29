import {
  Navigate,
  Outlet,
  Route,
  RouterProvider,
  Routes,
  createBrowserRouter,
  useLocation,
} from "react-router-dom";
import { useEffect, useRef } from "react";
import { AuthProvider, useAuth } from "../auth/AuthProvider";
import { LoginPage } from "../pages/LoginPage";
import { MachineAccessPage } from "../pages/MachineAccessPage";
import { MembersPage } from "../pages/MembersPage";
import { ProjectPage } from "../pages/ProjectPage";
import { ProjectsPage } from "../pages/ProjectsPage";
import { SystemPage } from "../pages/SystemPage";
import { AppShell } from "./AppShell";

export function App() {
  const routerRef = useRef<ReturnType<typeof createBrowserRouter> | null>(null);
  const mountedRef = useRef(false);
  if (routerRef.current === null) {
    routerRef.current = createBrowserRouter([
      { path: "*", element: <AppRoutes /> },
    ]);
  }
  const router = routerRef.current;

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      queueMicrotask(() => {
        if (!mountedRef.current) {
          router.dispose();
        }
      });
    };
  }, [router]);

  return <RouterProvider router={router} />;
}

function AppRoutes() {
  return (
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
                element={<MachineAccessPage />}
              />
              <Route
                path="/members"
                element={<MembersPage />}
              />
              <Route
                path="/system"
                element={<SystemPage />}
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
