import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { useProjects } from "../api/projects";
import { Page, SectionHead, EmptyState } from "../ui/Page";
import { Card, CardLink } from "../ui/Card";
import { Icon, type IconName } from "../ui/Icon";
import { cn } from "../ui/cn";

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
  node: { hostname: string; docker_version: string; compose_version: string; caddy_version: string };
  containers: { running: number; total: number };
}

function gb(bytes: number): string {
  return (bytes / (1 << 30)).toFixed(1) + " GB";
}

function Metric({
  label,
  icon,
  value,
  unit,
  sub,
  pct,
}: {
  label: string;
  icon: IconName;
  value: string;
  unit?: string;
  sub?: string;
  pct?: number | null;
}) {
  const warn = pct != null && pct >= 85;
  return (
    <Card className="flex flex-col gap-2.5 p-4">
      <div className="flex items-center justify-between">
        <span className="text-2xs font-semibold uppercase tracking-[0.04em] text-fg3">{label}</span>
        <span className="text-fg3">
          <Icon name={icon} size={16} />
        </span>
      </div>
      <div className="text-3xl font-semibold leading-none tracking-[-0.02em] tabular-nums">
        {value}
        {unit && <span className="ml-0.5 text-base font-medium text-fg3">{unit}</span>}
      </div>
      {pct != null && (
        <div className="h-[5px] overflow-hidden rounded-full bg-sunken">
          <div
            className={cn("h-full rounded-full", warn ? "bg-warn" : "bg-accent")}
            style={{ width: `${Math.min(100, Math.max(2, pct))}%` }}
          />
        </div>
      )}
      {sub && <div className="text-xs tabular-nums text-fg3">{sub}</div>}
    </Card>
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
  const memPct = m && m.host.memory_total > 0 ? Math.round((m.host.memory_used / m.host.memory_total) * 100) : null;
  const diskPct = m && m.host.disk_total > 0 ? Math.round((m.host.disk_used / m.host.disk_total) * 100) : null;

  return (
    <Page
      title={m?.node.hostname || "Dashboard"}
      subtitle={
        m
          ? `docker ${m.node.docker_version || "—"} · compose ${m.node.compose_version || "—"} · caddy ${m.node.caddy_version || "unavailable"}`
          : undefined
      }
    >
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Metric
          label="CPU"
          icon="dashboard"
          value={m ? `${m.host.cpu_percent.toFixed(0)}` : "—"}
          unit={m ? "%" : undefined}
          pct={m ? m.host.cpu_percent : null}
          sub={m ? `load ${m.host.load1.toFixed(2)}` : undefined}
        />
        <Metric
          label="Memory"
          icon="database"
          value={memPct !== null ? `${memPct}` : "—"}
          unit={memPct !== null ? "%" : undefined}
          pct={memPct}
          sub={m && m.host.memory_total > 0 ? `${gb(m.host.memory_used)} / ${gb(m.host.memory_total)}` : undefined}
        />
        <Metric
          label="Disk"
          icon="globe"
          value={diskPct !== null ? `${diskPct}` : "—"}
          unit={diskPct !== null ? "%" : undefined}
          pct={diskPct}
          sub={m && m.host.disk_total > 0 ? `${gb(m.host.disk_used)} / ${gb(m.host.disk_total)}` : undefined}
        />
        <Metric
          label="Containers"
          icon="projects"
          value={m ? `${m.containers.running}` : "—"}
          sub={m ? `of ${m.containers.total} total` : undefined}
        />
      </div>

      <SectionHead title="Projects" />
      {projects.data && projects.data.length === 0 ? (
        <EmptyState
          icon={<Icon name="projects" size={26} />}
          title="No projects yet"
          desc="Create one from Projects or spin up a database from Templates."
        />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {projects.data?.map((p) => (
            <CardLink key={p.name} to={`/projects/${p.name}`} className="p-4">
              <div className="flex items-center justify-between">
                <span className="text-md font-semibold tracking-[-0.01em]">{p.name}</span>
                <Icon name="chevronRight" size={16} className="text-fg3" />
              </div>
              <div className="mt-2 truncate font-mono text-xs text-fg3">{p.source}</div>
            </CardLink>
          ))}
        </div>
      )}
    </Page>
  );
}
