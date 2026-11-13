import type { ReactNode } from "react";
import { Link } from "react-router";
import { useCan } from "../api/auth";
import { useProjects } from "../api/projects";
import { PANEL_RSS_BUDGET_BYTES, useMetrics, useSystemDisk, useUpdateCheck, type SystemDisk } from "../api/system";
import { Page, EmptyState, MetaItem } from "../ui/Page";
import { Card } from "../ui/Card";
import { btn } from "../ui/Button";
import { StatusDot } from "../ui/Badge";
import { Icon, type IconName } from "../ui/Icon";
import { Meter, StackedMeter, usageTone, type MeterPart, type MeterTone } from "../ui/Meter";
import { Skeleton } from "../ui/Skeleton";
import { formatBytes, formatStamp, formatUptime, plural } from "../ui/format";
import ProjectsTable from "../components/ProjectsTable";
import NeedsYou from "../components/NeedsYou";

function Tile({
  label,
  icon,
  value,
  unit,
  meter,
  sub,
}: {
  label: string;
  icon: IconName;
  value?: string;
  unit?: string;
  meter?: ReactNode;
  sub?: ReactNode;
}) {
  return (
    <Card className="px-[18px] py-4">
      <div className="flex items-center gap-2 text-sm font-medium text-fg2">
        <Icon name={icon} size={16} className="text-fg3" />
        {label}
      </div>
      {value === undefined ? (
        <>
          <Skeleton className="mt-2.5 h-8 w-20" />
          <Skeleton className="mt-3 h-[5px] w-full" />
          <Skeleton className="mt-2 h-4 w-28" />
        </>
      ) : (
        <>
          <div className="mt-2.5 flex items-baseline gap-[3px]">
            <span className="text-3xl font-semibold tabular-nums tracking-[-0.03em] text-fg">{value}</span>
            {unit && <span className="text-md font-medium text-fg3">{unit}</span>}
          </div>
          {meter && <div className="mt-3">{meter}</div>}
          {sub && <div className="mt-2 truncate text-xs tabular-nums text-fg3">{sub}</div>}
        </>
      )}
    </Card>
  );
}

/** A card with the prototype's header: title, a short note, and a link on the right. */
function SideCard({ title, note, link, children, footer }: { title: string; note?: string; link?: { to: string; label: string }; children: ReactNode; footer?: ReactNode }) {
  return (
    <Card>
      <div className="flex items-center gap-3 px-[18px] py-3.5">
        <h2 className="text-md font-semibold tracking-[-0.01em] text-fg">{title}</h2>
        {note && <p className="text-xs text-fg3">{note}</p>}
        {link && (
          <Link to={link.to} className="ml-auto inline-flex min-h-6 items-center gap-0.5 text-xs font-semibold text-fg3 hover:text-fg">
            {link.label}
            <Icon name="chevronRight" size={13} />
          </Link>
        )}
      </div>
      {children}
      {footer && <p className="rounded-b-card border-t border-hairline bg-surface2 px-[18px] py-3 text-xs text-fg3">{footer}</p>}
    </Card>
  );
}

const DISK_COLOURS = {
  images: "var(--accent)",
  build: "var(--warn)",
  volumes: "var(--fg2)",
  other: "color-mix(in oklab, var(--fg) 22%, transparent)",
};

/**
 * What fills the projects volume. Docker's figures are its own; "Other" is
 * the rest of what the host reports used. Docker's data root can sit on a
 * different filesystem, so a negative remainder shows as nothing rather
 * than a wrong number.
 */
export function diskParts(disk: SystemDisk): MeterPart[] {
  const d = disk.docker;
  if (!d) return [];
  const docker = d.images.bytes + d.build_cache.bytes + d.volumes.bytes + d.containers.bytes;
  const other = Math.max(0, disk.host.used_bytes - docker);
  return [
    { label: "Images", bytes: d.images.bytes, colour: DISK_COLOURS.images },
    { label: "Build cache", bytes: d.build_cache.bytes, colour: DISK_COLOURS.build },
    { label: "Volumes", bytes: d.volumes.bytes, colour: DISK_COLOURS.volumes },
    { label: "System and other", bytes: other + d.containers.bytes, colour: DISK_COLOURS.other },
  ];
}

function partsLabel(parts: MeterPart[]): string {
  return parts.map((p) => `${p.label} ${formatBytes(p.bytes)}`).join(", ");
}

/**
 * The panel's memory against its idle budget. The budget is what CI holds an
 * idle process to; a panel that has just deployed or served the dashboard sits
 * above it for a while (66 MiB seen on the v0.62 test server, back under 40
 * within minutes), so only well over it is worth a colour: amber from twice,
 * red from three times.
 */
export function panelTone(pct: number): MeterTone {
  if (pct >= 300) return "err";
  if (pct >= 200) return "warn";
  return "ok";
}

export default function Dashboard() {
  const metrics = useMetrics(10_000);
  const disk = useSystemDisk();
  const projects = useProjects();
  const can = useCan();
  const update = useUpdateCheck(can("admin"));

  const m = metrics.data;
  const memPct = m && m.host.memory_total > 0 ? (m.host.memory_used / m.host.memory_total) * 100 : null;
  const diskPct = m && m.host.disk_total > 0 ? (m.host.disk_used / m.host.disk_total) * 100 : null;
  const rss = m?.panel?.rss_bytes ?? 0;
  const rssPct = (rss / PANEL_RSS_BUDGET_BYTES) * 100;
  const parts = disk.data ? diskParts(disk.data) : [];
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
      <div className="space-y-5">
        <NeedsYou />

        {metrics.isError && <p className="text-sm text-fg3">Host metrics are unavailable right now. Projects below are unaffected.</p>}

        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4 lg:gap-5">
          <Tile
            label="CPU"
            icon="cpu"
            value={m ? m.host.cpu_percent.toFixed(0) : undefined}
            unit="%"
            meter={m && <Meter label="CPU use" pct={m.host.cpu_percent} tone={usageTone(m.host.cpu_percent)} />}
            sub={m && `Load average ${m.host.load1.toFixed(2)}`}
          />
          <Tile
            label="Memory"
            icon="memory"
            value={memPct !== null ? memPct.toFixed(0) : m ? "0" : undefined}
            unit="%"
            meter={memPct !== null && <Meter label="Memory use" pct={memPct} tone={usageTone(memPct)} />}
            sub={m && m.host.memory_total > 0 && `${formatBytes(m.host.memory_used)} of ${formatBytes(m.host.memory_total)}`}
          />
          <Tile
            label="Disk"
            icon="disk"
            value={diskPct !== null ? diskPct.toFixed(0) : m ? "0" : undefined}
            unit="%"
            meter={
              diskPct !== null &&
              (parts.length > 0 && m ? (
                <StackedMeter parts={parts} total={m.host.disk_total} label={partsLabel(parts)} />
              ) : (
                <Meter label="Disk use" pct={diskPct} tone={usageTone(diskPct)} />
              ))
            }
            sub={
              m &&
              m.host.disk_total > 0 &&
              `${formatBytes(m.host.disk_used)} of ${formatBytes(m.host.disk_total)}, ${formatBytes(m.host.disk_total - m.host.disk_used)} free`
            }
          />
          <Tile
            label="Windlass"
            icon="activity"
            value={m ? (rss > 0 ? (rss / 1024 / 1024).toFixed(0) : "–") : undefined}
            unit={rss > 0 ? "MiB" : undefined}
            meter={rss > 0 && <Meter label="Panel memory against its idle budget" pct={rssPct} tone={panelTone(rssPct)} />}
            sub={m && (rss > 0 ? `Idle budget ${Math.round(PANEL_RSS_BUDGET_BYTES / 2 ** 20)} MiB` : "Panel memory is not readable here")}
          />
        </div>

        <div className="grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1fr)_340px]">
          <section className="min-w-0">
            {projects.data && list.length === 0 ? (
              <>
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
                <p className="mt-3 text-sm text-fg3">
                  Using an AI coding agent?{" "}
                  <Link to="/agents" className="font-medium text-accent hover:underline">
                    Connect it to this server
                  </Link>{" "}
                  to check status, read logs and deploy.
                </p>
              </>
            ) : (
              <SideCard
                title="Projects"
                note={projects.data && (m ? `${plural(list.length, "project")}, ${plural(m.containers.total, "container")}` : plural(list.length, "project"))}
                link={{ to: "/projects", label: "All projects" }}
              >
                <ProjectsTable projects={list} loading={projects.isLoading} embedded />
              </SideCard>
            )}
          </section>

          <div className="min-w-0 space-y-5">
            <SideCard title="Disk" note="Docker's share" link={{ to: "/settings/system", label: "Details" }}>
              <DiskRows disk={disk.data} loading={disk.isLoading} />
            </SideCard>

            <SideCard
              title="Server"
              link={can("admin") ? { to: "/settings/system", label: "Settings" } : undefined}
              footer="Containers belong to Docker, not Windlass. Stop the panel and every project keeps running."
            >
              <dl className="border-t border-hairline">
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
                {update.data && (
                  <Row label="Windlass">
                    {update.data.current_version}
                    {update.data.update_available ? `, ${update.data.version} available` : ", latest"}
                  </Row>
                )}
              </dl>
            </SideCard>
          </div>
        </div>
      </div>
    </Page>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-hairline px-[18px] py-2.5 text-sm last:border-b-0">
      <dt className="text-fg3">{label}</dt>
      <dd className="min-w-0 truncate text-right font-medium text-fg">{children}</dd>
    </div>
  );
}

function DiskRows({ disk, loading }: { disk?: SystemDisk; loading: boolean }) {
  if (loading) {
    return (
      <div className="space-y-2 border-t border-hairline px-[18px] py-3">
        <Skeleton className="h-5 w-2/3" />
        <Skeleton className="h-5 w-1/2" />
      </div>
    );
  }
  if (!disk?.docker) {
    return <p className="border-t border-hairline px-[18px] py-3 text-sm text-fg3">Docker's disk figures are unavailable right now.</p>;
  }
  const d = disk.docker;
  const parts = diskParts(disk);
  const notes: Record<string, string | undefined> = {
    Images: d.images.reclaimable_bytes > 0 ? `${formatBytes(d.images.reclaimable_bytes)} unused by any container` : undefined,
    "Build cache": d.build_cache.reclaimable_bytes > 0 ? `${formatBytes(d.build_cache.reclaimable_bytes)} reclaimable` : undefined,
    Volumes:
      d.volumes.count > d.volumes.active_count
        ? `${plural(d.volumes.count, "volume")}, ${d.volumes.count - d.volumes.active_count} not used by a container`
        : plural(d.volumes.count, "volume"),
  };
  return (
    <div className="border-t border-hairline">
      {parts.map((p) => (
        <div key={p.label} className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-px border-b border-hairline px-[18px] py-2.5 text-sm last:border-b-0">
          <span className="flex items-center gap-2 text-fg2">
            <i className="h-2.5 w-2.5 flex-none rounded-full" style={{ background: p.colour }} aria-hidden="true" />
            {p.label}
          </span>
          <b className="font-semibold tabular-nums text-fg">{formatBytes(p.bytes)}</b>
          {notes[p.label] && <small className="col-span-2 pl-[18px] text-xs text-fg3">{notes[p.label]}</small>}
        </div>
      ))}
      {disk.checked_at && (
        <p className="px-[18px] py-2 text-xs text-fg3" title={formatStamp(disk.checked_at)}>
          Docker figures as of {formatStamp(disk.checked_at).slice(-5)}
        </p>
      )}
    </div>
  );
}
