import { Routes, Route } from "react-router-dom";
import { useAuthStatus } from "./api/auth";
import Login from "./pages/Login";
import Setup from "./pages/Setup";
import Layout from "./components/Layout";
import Dashboard from "./pages/Dashboard";
import Projects from "./pages/Projects";
import ProjectDetail from "./pages/ProjectDetail";
import Settings from "./pages/Settings";
import Templates from "./pages/Templates";
import RouteErrorBoundary from "./components/RouteErrorBoundary";
import { Page } from "./ui/Page";
import { Spinner } from "./ui/Spinner";

export default function App() {
  const status = useAuthStatus();

  if (status.isLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center gap-3 bg-canvas text-sm text-fg3">
        <Spinner /> Loading…
      </div>
    );
  }
  if (status.isError) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-canvas text-sm text-err">
        Server unreachable
      </div>
    );
  }

  const auth = status.data!;
  if (auth.needs_setup) return <Setup />;
  if (!auth.authenticated || !auth.user) return <Login />;

  return (
    <Routes>
      <Route element={<Layout user={auth.user} />}>
        <Route index element={<Dashboard />} />
        <Route path="projects" element={<Projects />} />
        <Route
          path="projects/:name/*"
          element={
            <RouteErrorBoundary>
              <ProjectDetail />
            </RouteErrorBoundary>
          }
        />
        <Route path="templates" element={<Templates />} />
        <Route path="settings" element={<Settings />} />
        <Route path="*" element={<Placeholder title="Not found" />} />
      </Route>
    </Routes>
  );
}

function Placeholder({ title }: { title: string }) {
  return (
    <Page title={title}>
      <p className="text-sm text-fg2">Coming in a later milestone.</p>
    </Page>
  );
}
