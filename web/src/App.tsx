import { useQuery } from "@tanstack/react-query";
import { Routes, Route } from "react-router-dom";

interface Health {
  status: string;
  version: string;
}

function Dashboard() {
  const { data, isLoading, isError } = useQuery<Health>({
    queryKey: ["system", "health"],
    queryFn: async () => {
      const res = await fetch("/api/v1/system/health");
      if (!res.ok) throw new Error(`health check failed: ${res.status}`);
      return res.json();
    },
  });

  return (
    <div className="flex min-h-screen items-center justify-center bg-zinc-950 text-zinc-100">
      <div className="text-center">
        <h1 className="text-4xl font-semibold tracking-tight">Windlass</h1>
        <p className="mt-2 text-zinc-400">Docker Compose control plane</p>
        <div className="mt-6 text-sm">
          {isLoading && <span className="text-zinc-500">Checking server…</span>}
          {isError && <span className="text-red-400">Server unreachable</span>}
          {data && (
            <span className="text-emerald-400">
              {data.status} · v{data.version}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}

export default function App() {
  return (
    <Routes>
      <Route path="*" element={<Dashboard />} />
    </Routes>
  );
}
