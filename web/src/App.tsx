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
import NotFound from "./pages/NotFound";
import RouteErrorBoundary from "./components/RouteErrorBoundary";
import { WindlassMark } from "./ui/Logo";
import { Button } from "./ui/Button";

export default function App() {
  const status = useAuthStatus();

  if (status.isLoading) {
    return (
      <div className="grid min-h-dvh place-items-center bg-canvas" aria-busy="true" aria-label="Loading">
        <WindlassMark size={30} className="text-accent opacity-80 [animation:wl-pulse_1.6s_var(--ease)_infinite]" />
      </div>
    );
  }
  if (status.isError) {
    return (
      <div className="grid min-h-dvh place-items-center bg-canvas px-4">
        <div className="max-w-sm text-center">
          <WindlassMark size={30} className="mx-auto text-fg3" />
          <h1 className="mt-4 text-lg font-semibold text-fg">Can't reach Windlass</h1>
          <p className="mt-1 text-sm text-fg3">
            The panel's server isn't answering. Deployed apps are unaffected; they run without it.
          </p>
          <Button className="mt-5" icon="refresh" onClick={() => status.refetch()} loading={status.isFetching}>
            Try again
          </Button>
        </div>
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
        <Route path="settings/:tab" element={<Settings />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
