import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";

interface Health {
  status: string;
  version: string;
}

export default function Dashboard() {
  const health = useQuery<Health>({
    queryKey: ["system", "health"],
    queryFn: () => api<Health>("/system/health"),
  });

  return (
    <div>
      <h1 className="text-xl font-semibold">Dashboard</h1>
      <div className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <div className="rounded-lg border border-zinc-900 bg-zinc-900/50 p-4">
          <div className="text-xs uppercase tracking-wide text-zinc-500">
            Server
          </div>
          <div className="mt-2 text-sm">
            {health.data ? (
              <span className="text-emerald-400">
                {health.data.status} · v{health.data.version}
              </span>
            ) : (
              <span className="text-zinc-500">checking…</span>
            )}
          </div>
        </div>
      </div>
      <p className="mt-8 text-sm text-zinc-600">
        Host metrics, projects, and recent deployments land here in upcoming
        milestones.
      </p>
    </div>
  );
}
