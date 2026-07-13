import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import { useProjects } from "../api/projects";

interface Metrics {
  host: {
    cpu_percent: number;
    memory_used: number;
    memory_total: number;
    disk_used: number;
    disk_total: number;
    load1: number;
    uptime_seconds: number;
  };
  node: {
    hostname: string;
    docker_version: string;
    compose_version: string;
    caddy_version: string;
  };
  containers: { running: number; total: number };
}

function gb(bytes: number): string {
  return (bytes / (1 << 30)).toFixed(1) + " GB";
}

function Card({ title, value, sub }: { title: string; value: string; sub?: string }) {
  return (
    <div className="rounded-lg border border-zinc-900 bg-zinc-900/50 p-4">
      <div className="text-xs uppercase tracking-wide text-zinc-500">{title}</div>
      <div className="mt-2 text-lg font-semibold">{value}</div>
      {sub && <div className="mt-0.5 text-xs text-zinc-500">{sub}</div>}
    </div>
  );
}

export default function Dashboard() {
  const metrics = useQuery<Metrics>({
    queryKey: ["system", "metrics"],
    queryFn: () => api("/system/metrics"),
    refetchInterval: 10_000,
  });
  const projects = useProjects();

  const m = metrics.data;
  const memPct =
    m && m.host.memory_total > 0
      ? Math.round((m.host.memory_used / m.host.memory_total) * 100)
      : null;
  const diskPct =
    m && m.host.disk_total > 0
      ? Math.round((m.host.disk_used / m.host.disk_total) * 100)
      : null;

  return (
    <div>
      <h1 className="text-xl font-semibold">
        {m?.node.hostname || "Dashboard"}
      </h1>
      {m && (
        <p className="mt-1 text-xs text-zinc-600">
          docker {m.node.docker_version || "—"} · compose{" "}
          {m.node.compose_version || "—"} · caddy {m.node.caddy_version || "unavailable"}
        </p>
      )}

      <div className="mt-6 grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Card
          title="CPU"
          value={m ? `${m.host.cpu_percent.toFixed(0)}%` : "—"}
          sub={m ? `load ${m.host.load1.toFixed(2)}` : undefined}
        />
        <Card
          title="Memory"
          value={memPct !== null ? `${memPct}%` : "—"}
          sub={m && m.host.memory_total > 0 ? `${gb(m.host.memory_used)} / ${gb(m.host.memory_total)}` : undefined}
        />
        <Card
          title="Disk"
          value={diskPct !== null ? `${diskPct}%` : "—"}
          sub={m && m.host.disk_total > 0 ? `${gb(m.host.disk_used)} / ${gb(m.host.disk_total)}` : undefined}
        />
        <Card
          title="Containers"
          value={m ? `${m.containers.running}` : "—"}
          sub={m ? `${m.containers.total} total` : undefined}
        />
      </div>

      <h2 className="mt-10 text-base font-medium">Projects</h2>
      <div className="mt-3 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {projects.data?.map((p) => (
          <Link
            key={p.name}
            to={`/projects/${p.name}`}
            className="rounded-lg border border-zinc-900 bg-zinc-900/50 p-4 hover:border-zinc-700"
          >
            <div className="font-medium">{p.name}</div>
            <div className="mt-1 text-xs text-zinc-500">{p.source}</div>
          </Link>
        ))}
        {projects.data?.length === 0 && (
          <p className="text-sm text-zinc-600">
            No projects yet — create one from Projects or Templates.
          </p>
        )}
      </div>
    </div>
  );
}
