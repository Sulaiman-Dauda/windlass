import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { useProject } from "../api/projects";
import { useDeployments, useServices, deploymentTone, isActive } from "../api/deployments";
import { useDomains } from "../api/domains";
import { Card, CardHeader, CardFooter } from "../ui/Card";
import { StatusPill, StatusDot, type Tone } from "../ui/Badge";
import { Callout } from "../ui/Page";
import { Icon } from "../ui/Icon";
import { Skeleton } from "../ui/Skeleton";
import { elapsed, formatBytes, formatDuration, timeAgo } from "../ui/format";
import { repoName } from "./ProjectsTable";

function stateTone(state: string): { tone: Tone; live?: boolean } {
  switch (state) {
    case "running":
      return { tone: "ok", live: true };
    case "restarting":
      return { tone: "warn" };
    case "dead":
      return { tone: "err" };
    default:
      return { tone: "idle" };
  }
}

function healthTone(health: string): Tone {
  if (health === "healthy") return "ok";
  if (health === "unhealthy") return "err";
  if (health === "starting") return "warn";
  return "idle";
}

export default function OverviewTab({ project }: { project: string }) {
  const services = useServices(project);
  const list = services.data?.services ?? [];

  return (
    <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_340px]">
      <div className="min-w-0 space-y-4">
        {services.data?.note && (
          <Callout tone="warn" title="Compose couldn't report on this project">
            <span className="break-words font-mono text-xs">{services.data.note}</span>
          </Callout>
        )}
        <Card>
          <CardHeader
            title="Services"
            description="Live from docker compose ps, refreshed every few seconds."
          />
          <div className="overflow-x-auto">
            <table className="w-full min-w-[540px] table-fixed text-sm">
              <colgroup>
                <col />
                <col className="w-[150px]" />
                <col />
                <col className="w-[168px]" />
              </colgroup>
              <thead>
                <tr className="border-b border-hairline bg-surface2 text-left text-xs text-fg3">
                  <th className="h-9 px-5 font-medium">Service</th>
                  <th className="h-9 px-4 font-medium">Status</th>
                  <th className="h-9 px-4 font-medium">Image</th>
                  <th className="h-9 px-5 text-right font-medium">Limits</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-hairline">
                {services.isLoading &&
                  [0, 1].map((i) => (
                    <tr key={i}>
                      <td colSpan={4} className="px-5 py-3.5">
                        <Skeleton className="h-5 w-1/2" />
                      </td>
                    </tr>
                  ))}
                {list.map((s) => {
                  const st = stateTone(s.state);
                  return (
                    <tr key={s.name} className="transition-colors hover:bg-surface2">
                      <td className="px-5 py-3">
                        <div className="truncate font-semibold text-fg">{s.service}</div>
                        <div className="truncate font-mono text-xs text-fg3" title={s.name}>
                          {s.name}
                        </div>
                      </td>
                      <td className="px-4 py-3">
                        <StatusPill tone={st.tone} live={st.live}>
                          {s.state}
                          {s.state === "exited" && s.exit_code !== 0 && (
                            <span className="font-medium opacity-75">({s.exit_code})</span>
                          )}
                        </StatusPill>
                        {s.health ? (
                          <div className="mt-1 flex items-center gap-1.5 text-xs text-fg2">
                            <StatusDot tone={healthTone(s.health)} />
                            {s.health}
                          </div>
                        ) : (
                          <div className="mt-1 text-xs text-fg3">No health check</div>
                        )}
                      </td>
                      <td className="px-4 py-3">
                        <span className="block truncate font-mono text-xs text-fg2" title={s.image}>
                          {s.image}
                        </span>
                      </td>
                      <td className="whitespace-nowrap px-5 py-3 text-right text-xs text-fg2">
                        {s.memory_limit ? formatBytes(s.memory_limit) : <span className="text-fg3">No memory cap</span>}
                        {s.cpu_limit ? ` · ${s.cpu_limit} CPU` : ""}
                      </td>
                    </tr>
                  );
                })}
                {services.data && list.length === 0 && (
                  <tr>
                    <td colSpan={4} className="px-5 py-10 text-center text-sm text-fg3">
                      No containers yet. Deploy the project to start its services.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
          <CardFooter
            note={
              <>
                Limits come from <code className="font-mono text-fg2">mem_limit</code> and{" "}
                <code className="font-mono text-fg2">cpus</code> in{" "}
                <Link className="text-accent hover:underline" to="files">
                  compose.yaml
                </Link>
                . Gate readiness with the labels <code className="font-mono text-fg2">windlass.health.url</code>,{" "}
                <code className="font-mono text-fg2">windlass.health.status</code> and{" "}
                <code className="font-mono text-fg2">windlass.health.contains</code>.
              </>
            }
          />
        </Card>
      </div>

      <div className="min-w-0 space-y-4">
        <LatestDeployment project={project} />
        <DomainsSummary project={project} />
        <SourceSummary project={project} />
      </div>
    </div>
  );
}

function SideCard({ title, link, children }: { title: string; link?: { to: string; label: string }; children: ReactNode }) {
  return (
    <Card>
      <div className="flex items-center justify-between border-b border-hairline px-4 py-3">
        <h3 className="text-sm font-semibold text-fg">{title}</h3>
        {link && (
          <Link to={link.to} className="inline-flex items-center gap-0.5 text-xs font-semibold text-fg3 hover:text-fg">
            {link.label}
            <Icon name="chevronRight" size={13} />
          </Link>
        )}
      </div>
      <div className="px-4 py-3.5">{children}</div>
    </Card>
  );
}

function LatestDeployment({ project }: { project: string }) {
  const deployments = useDeployments(project);
  const d = deployments.data?.[0];
  if (!deployments.data) return <Skeleton className="h-[132px] w-full rounded-card" />;
  if (!d) {
    return (
      <SideCard title="Latest deployment">
        <p className="text-sm text-fg3">Not deployed yet. Deploy runs the pipeline and streams its log.</p>
      </SideCard>
    );
  }
  const ms = elapsed(d.started_at || d.created_at, d.finished_at);
  return (
    <SideCard title="Latest deployment" link={{ to: `deployments?d=${d.number}`, label: "View log" }}>
      <div className="flex items-center justify-between gap-3">
        <span className="text-md font-semibold text-fg">#{d.number}</span>
        <StatusPill tone={deploymentTone(d.status)} busy={isActive(d.status)}>
          {d.status}
        </StatusPill>
      </div>
      <dl className="mt-3 space-y-1.5 text-sm">
        <div className="flex justify-between gap-3">
          <dt className="text-fg3">Triggered by</dt>
          <dd className="truncate text-fg2">{d.triggered_by}</dd>
        </div>
        {d.git_commit && (
          <div className="flex justify-between gap-3">
            <dt className="text-fg3">Commit</dt>
            <dd className="font-mono text-xs text-fg2">{d.git_commit.slice(0, 7)}</dd>
          </div>
        )}
        <div className="flex justify-between gap-3">
          <dt className="text-fg3">{isActive(d.status) ? "Running for" : "Took"}</dt>
          <dd className="tabular-nums text-fg2">{ms !== null ? formatDuration(ms) : "–"}</dd>
        </div>
        <div className="flex justify-between gap-3">
          <dt className="text-fg3">When</dt>
          <dd className="text-fg2">{timeAgo(d.created_at)}</dd>
        </div>
      </dl>
      {d.status === "failed" && d.error && <p className="mt-3 line-clamp-3 break-words text-xs text-err">{d.error}</p>}
    </SideCard>
  );
}

function DomainsSummary({ project }: { project: string }) {
  const domains = useDomains(project);
  return (
    <SideCard title="Domains" link={{ to: "domains", label: "Manage" }}>
      {!domains.data ? (
        <Skeleton className="h-5 w-2/3" />
      ) : domains.data.length === 0 ? (
        <p className="text-sm text-fg3">No domains. Add one and Caddy serves it over HTTPS.</p>
      ) : (
        <ul className="space-y-2">
          {domains.data.map((d) => (
            <li key={d.hostname} className="flex items-center gap-2 text-sm">
              <StatusDot tone={d.status === "active" ? "ok" : d.status === "pending" ? "warn" : "err"} />
              <a
                href={`https://${d.hostname}`}
                target="_blank"
                rel="noreferrer"
                className="min-w-0 flex-1 truncate font-medium text-fg hover:text-accent"
              >
                {d.hostname}
              </a>
              <span className="font-mono text-xs text-fg3">
                {d.service}:{d.container_port}
              </span>
            </li>
          ))}
        </ul>
      )}
    </SideCard>
  );
}

function SourceSummary({ project }: { project: string }) {
  const p = useProject(project).data;
  return (
    <SideCard title="Source" link={{ to: "git", label: "Configure" }}>
      {!p ? (
        <Skeleton className="h-5 w-2/3" />
      ) : p.git_repo ? (
        <div className="space-y-1.5 text-sm">
          <div className="flex items-center gap-2 font-medium text-fg">
            <Icon name="gitBranch" size={15} className="text-fg3" />
            <span className="truncate">{repoName(p.git_repo)}</span>
          </div>
          <p className="text-fg3">
            Branch <span className="font-mono text-xs text-fg2">{p.git_branch || "main"}</span>
            {p.auto_deploy ? ", deploys on every push." : ", deploys when you press Deploy."}
          </p>
        </div>
      ) : (
        <p className="text-sm text-fg3">
          Compose files on disk. Edit them in{" "}
          <Link to="files" className="text-accent hover:underline">
            Files
          </Link>
          , or connect a repository to deploy on push.
        </p>
      )}
    </SideCard>
  );
}
