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

export default function App() {
  const status = useAuthStatus();

  if (status.isLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-zinc-950 text-sm text-zinc-600">
        Loading…
      </div>
    );
  }
  if (status.isError) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-zinc-950 text-sm text-red-400">
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
        <Route path="projects/:name/*" element={<ProjectDetail />} />
        <Route path="templates" element={<Templates />} />
        <Route path="settings" element={<Settings />} />
        <Route path="*" element={<Placeholder title="Not found" />} />
      </Route>
    </Routes>
  );
}

function Placeholder({ title }: { title: string }) {
  return (
    <div>
      <h1 className="text-xl font-semibold">{title}</h1>
      <p className="mt-2 text-sm text-zinc-500">Coming in a later milestone.</p>
    </div>
  );
}
