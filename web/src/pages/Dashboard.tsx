import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { useCan } from "../api/auth";
import { useProjects } from "../api/projects";
import { useMetrics } from "../api/system";
import { Page, SectionHead, EmptyState, MetaItem } from "../ui/Page";
import { Card } from "../ui/Card";
import { btn } from "../ui/Button";
import { StatusDot } from "../ui/Badge";
import { Icon, type IconName } from "../ui/Icon";
import { Skeleton } from "../ui/Skeleton";
import { cn } from "../ui/cn";
import { formatBytes, formatUptime, plural } from "../ui/format";
import ProjectsTable from "../components/ProjectsTable";

function Meter({ pct }: { pct: number }) {
  const tone = pct >= 95 ? "bg-err" : pct >= 85 ? "bg-warn" : "bg-accent";
  return (
    <div
      className="mt-3 h-1.5 overflow-hidden rounded-full bg-sunken"
      role="meter"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(pct)}
    >
      <div
        className={cn("h-full rounded-full transition-[width] duration-700", tone)}
        style={{ width: `${Math.min(100, Math.max(1.5, pct))}%`, transitionTimingFunction: "var(--ease)" }}
      />
    </div>
  );
}

function Tile({
  label,
  icon,
  value,
  unit,
  pct,
  sub,
}: {
  label: string;
  icon: IconName;
  value?: string;
  unit?: string;
  pct?: number | null;
  sub?: ReactNode;
}) {
  return (
    <Card className="p-4">
      <div className="flex items-center gap-2 text-sm font-medium text-fg2">
        <Icon name={icon} size={16} className="text-fg3" />
        {label}
      </div>
      {value === undefined ? (
        <>
          <Skeleton className="mt-3 h-8 w-20" />
          <Skeleton className="mt-3 h-1.5 w-full" />
          <Skeleton className="mt-2.5 h-4 w-28" />
        </>
      ) : (
        <>
          <div className="mt-2.5 flex items-baseline gap-1">
            <span className="text-3xl font-semibold tabular-nums tracking-[-0.03em] text-fg">{value}</span>
            {unit && <span className="text-md font-medium text-fg3">{unit}</span>}
          </div>
          {pct != null && <Meter pct={pct} />}
          {sub && <div className="mt-2 text-xs tabular-nums text-fg3">{sub}</div>}
        </>
      )}
    </Card>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4 px-4 py-2.5 text-sm">
      <dt className="text-fg3">{label}</dt>
      <dd className="min-w-0 truncate text-right font-medium text-fg">{children}</dd>
    </div>
  );
}

export default function Dashboard() {
  const metrics = useMetrics(10_000);
  const projects = useProjects();
  const can = useCan();

  const m = metrics.data;
  const memPct = m && m.host.memory_total > 0 ? (m.host.memory_used / m.host.memory_total) * 100 : null;
  const diskPct = m && m.host.disk_total > 0 ? (m.host.disk_used / m.host.disk_total) * 100 : null;
  const stopped = m ? m.containers.total - m.containers.running : 0;
  const list = projects.data ?? [];

  return (
    <Page
      title="Overview"
      crumbs={[{ label: "Overview" }]}
      meta={
        m && (
          <>
            <MetaItem icon="server">{m.node.hostname}</MetaItem>
            {m.host.uptime_seconds > 0 && <MetaItem icon="clock">Up {formatUptime(m.host.uptime_seconds)}</MetaItem>}
            <MetaItem icon="activity">Load {m.host.load1.toFixed(2)}</MetaItem>
          </>
        )
      }
      actions={
        can("member") && (
          <>
            <Link to="/templates" className={btn("secondary", "md")}>
              <Icon name="templates" size={16} /> Templates
            </Link>
            <Link to="/projects?new=1" className={btn("primary", "md")}>
              <Icon name="plus" size={16} /> New project
            </Link>
          </>
        )
      }
    >
      {metrics.isError && (
        <p className="mb-4 text-sm text-fg3">Host metrics are unavailable right now. Projects below are unaffected.</p>
      )}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Tile
          label="CPU"
          icon="cpu"
          value={m ? m.host.cpu_percent.toFixed(0) : undefined}
          unit="%"
          pct={m?.host.cpu_percent}
          sub={m && `Load average ${m.host.load1.toFixed(2)}`}
        />
        <Tile
          label="Memory"
          icon="memory"
          value={memPct !== null ? memPct.toFixed(0) : m ? "0" : undefined}
          unit="%"
          pct={memPct}
          sub={m && m.host.memory_total > 0 && `${formatBytes(m.host.memory_used)} of ${formatBytes(m.host.memory_total)}`}
        />
        <Tile
          label="Disk"
          icon="disk"
          value={diskPct !== null ? diskPct.toFixed(0) : m ? "0" : undefined}
          unit="%"
          pct={diskPct}
          sub={m && m.host.disk_total > 0 && `${formatBytes(m.host.disk_used)} of ${formatBytes(m.host.disk_total)}`}
        />
        <Tile
          label="Containers"
          icon="layers"
          value={m ? String(m.containers.running) : undefined}
          unit={m ? `/ ${m.containers.total}` : undefined}
          pct={m && m.containers.total > 0 ? (m.containers.running / m.containers.total) * 100 : null}
          sub={m && (stopped > 0 ? `${stopped} stopped` : "All running")}
        />
      </div>

      <div className="mt-8 grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_320px]">
        <section className="min-w-0">
          <SectionHead
            title="Projects"
            description={projects.data && plural(list.length, "project")}
            actions={
              list.length > 0 && (
                <Link to="/projects" className="inline-flex items-center gap-1 text-sm font-medium text-fg3 hover:text-fg">
                  View all <Icon name="chevronRight" size={14} />
                </Link>
              )
            }
          />
          {projects.data && list.length === 0 ? (
            <EmptyState
              icon="projects"
              title="No projects yet"
              desc="A project is a directory with a compose.yaml. Create one, start from a template, or scan the stacks directory to adopt what is already there."
              actions={
                can("member") && (
                  <>
                    <Link to="/templates" className={btn("secondary", "md")}>
                      Browse templates
                    </Link>
                    <Link to="/projects?new=1" className={btn("primary", "md")}>
                      <Icon name="plus" size={16} /> New project
                    </Link>
                  </>
                )
              }
            />
          ) : (
            <ProjectsTable projects={list} loading={projects.isLoading} />
          )}
        </section>

        <section className="min-w-0">
          <SectionHead title="Server" description="Runtime this panel drives" />
          <Card>
            <dl className="divide-y divide-hairline">
              <Row label="Hostname">{m?.node.hostname || "–"}</Row>
              <Row label="Uptime">{m ? formatUptime(m.host.uptime_seconds) || "–" : "–"}</Row>
              <Row label="Docker">{m?.node.docker_version || "–"}</Row>
              <Row label="Compose">{m?.node.compose_version || "–"}</Row>
              <Row label="Caddy">
                {m ? (
                  <span className="inline-flex items-center gap-2">
                    <StatusDot tone={m.node.caddy_version ? "ok" : "warn"} />
                    {m.node.caddy_version || "Unavailable"}
                  </span>
                ) : (
                  "–"
                )}
              </Row>
            </dl>
          </Card>
          <p className="mt-3 px-1 text-xs leading-relaxed text-fg3">
            Containers belong to Docker, not to Windlass. Stop the panel and every project keeps running.
          </p>
        </section>
      </div>
    </Page>
  );
}
