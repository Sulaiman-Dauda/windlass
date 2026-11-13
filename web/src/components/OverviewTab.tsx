import { useState, type ReactNode } from "react";
import { useCan } from "../api/auth";
import { Link } from "react-router";
import { useProject } from "../api/projects";
import {
  useCreateDeployment,
  useDeployments,
  useServices,
  deploymentTone,
  isActive,
  isCompleted,
  stageLabel,
  type Deployment,
} from "../api/deployments";
import { useDomains } from "../api/domains";
import { Card, CardHeader, CardFooter } from "../ui/Card";
import { StatusPill, StatusDot, type Tone } from "../ui/Badge";
import { Callout } from "../ui/Page";
import { Icon } from "../ui/Icon";
import { Skeleton } from "../ui/Skeleton";
import { elapsed, formatBytes, formatDuration, formatStamp, timeAgo } from "../ui/format";
import { btn } from "../ui/Button";
import { useToast, errorText } from "../ui/Toast";
import { repoName } from "./ProjectsTable";
import StagingCard from "./StagingCard";
import CreateStagingDialog from "./CreateStagingDialog";
import { Button } from "../ui/Button";

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
        <FailedDeployNotice project={project} />
        <StagingCard project={project} />
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
                  const done = isCompleted(s);
                  const st = done ? { tone: "ok" as const } : stateTone(s.state);
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
                          {done ? "completed" : s.state}
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
                        {s.cpu_limit ? `, ${s.cpu_limit} CPU` : ""}
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
                Limits and health checks come from{" "}
                <Link className="text-accent hover:underline" to={`/projects/${project}/files`}>
                  compose.yaml
                </Link>
                . Without a health check, running is all Windlass can see; the label{" "}
                <code className="font-mono text-fg2">windlass.health.url</code> gates a deploy on a URL.
              </>
            }
          />
        </Card>
      </div>

      <div className="min-w-0 space-y-4">
        <LatestDeployment project={project} />
        <DomainsSummary project={project} />
        <SourceSummary project={project} />
        <StagingOffer project={project} />
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
          <Link to={link.to} className="inline-flex min-h-6 items-center gap-0.5 text-xs font-semibold text-fg3 hover:text-fg">
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
    <SideCard title="Latest deployment" link={{ to: `/projects/${project}/deployments?d=${d.number}`, label: "View log" }}>
      <div className="flex items-center justify-between gap-3">
        <span className="text-md font-semibold text-fg">#{d.number}</span>
        <StatusPill tone={deploymentTone(d.status)} busy={isActive(d.status)}>
          {d.status}
        </StatusPill>
      </div>
      <dl className="mt-3 space-y-1.5 text-sm">
        <div className="flex justify-between gap-3">
          <dt className="text-fg3">Triggered by</dt>
          <dd className="truncate text-fg2">{d.promoted_from_number ? `promoted from staging #${d.promoted_from_number}` : d.triggered_by}</dd>
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
          <dd className="text-fg2" title={formatStamp(d.created_at)}>
            {timeAgo(d.created_at)}
          </dd>
        </div>
      </dl>
    </SideCard>
  );
}

function DomainsSummary({ project }: { project: string }) {
  const domains = useDomains(project);
  return (
    <SideCard title="Domains" link={{ to: `/projects/${project}/domains`, label: "Manage" }}>
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
    <SideCard title="Source" link={{ to: `/projects/${project}/git`, label: "Configure" }}>
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
            {p.staging ? (
              <>
                , deploys through{" "}
                <Link to={`/projects/${p.staging}`} className="text-accent hover:underline">
                  {p.staging}
                </Link>
                . Pushes go there first.
              </>
            ) : p.auto_deploy ? (
              ", deploys on every push."
            ) : (
              ", deploys when you press Deploy."
            )}
          </p>
        </div>
      ) : (
        <p className="text-sm text-fg3">
          Compose files on disk. Edit them in{" "}
          <Link to={`/projects/${project}/files`} className="text-accent hover:underline">
            Files
          </Link>
          , or connect a repository to deploy on push.
        </p>
      )}
    </SideCard>
  );
}

/** The offer to create a staging, for a member on a project that has none and is not one. */
function StagingOffer({ project }: { project: string }) {
  const p = useProject(project).data;
  const can = useCan();
  const [open, setOpen] = useState(false);
  if (!p || p.staging || p.staging_of || !can("member")) return null;
  return (
    <SideCard title="Staging">
      <p className="text-sm text-fg3">
        Try changes on a copy of {project} with its own domains and data. Pushes then deploy to the copy, and {project} changes
        only when you promote what passed.
      </p>
      <Button size="sm" icon="layers" className="mt-3" onClick={() => setOpen(true)}>
        Create staging
      </Button>
      {open && <CreateStagingDialog live={project} onClose={() => setOpen(false)} />}
    </SideCard>
  );
}

// Steps that run before any container is touched: a failure in one of them
// leaves the running containers as they were.
const BEFORE_APPLY = ["preparing", "syncing", "pulling", "building"];

/** What a failed deploy left running, in one sentence. */
export function failureImpact(failed: Deployment, live: Deployment | undefined, domain?: string): string {
  const step = failed.failed_step ?? "";
  if (!live) return "No deploy of this project has succeeded yet, so nothing earlier is running.";
  if (BEFORE_APPLY.includes(step)) {
    return `The running containers did not change. #${live.number} is still running${domain ? ` and serving ${domain}` : ""}.`;
  }
  if (step === "applying" || step === "verifying") {
    return `It failed while starting services, so some may be on the new version. #${live.number} was the last deploy that succeeded.`;
  }
  return `#${live.number} was the last deploy that succeeded.`;
}

/** A GitHub commit page for an https GitHub remote, else nothing. */
export function commitUrl(repo: string | undefined, sha: string | undefined): string | undefined {
  if (!repo || !sha) return undefined;
  const m = repo.match(/^https:\/\/github\.com\/([^/]+\/[^/]+?)(?:\.git)?\/?$/);
  return m ? `https://github.com/${m[1]}/commit/${sha}` : undefined;
}

/** Shown while the project's newest deployment is a failure. */
function FailedDeployNotice({ project }: { project: string }) {
  const deployments = useDeployments(project);
  const domains = useDomains(project);
  const p = useProject(project).data;
  const deploy = useCreateDeployment(project);
  const can = useCan();
  const toast = useToast();
  const list = deployments.data ?? [];
  const d = list[0];
  if (!d || d.status !== "failed") return null;

  const live = list.find((x) => x.status === "succeeded");
  const step = d.failed_step ? stageLabel(d.failed_step) : "";
  const line = d.error_line || d.error;
  const when = d.finished_at || d.created_at;
  const commit = commitUrl(p?.git_repo, d.git_commit);
  // A project with a staging confirms before a direct deploy; that dialog
  // lives on the header's Deploy button, so Retry is not offered here.
  const canRetry = can("member") && !p?.staging;

  return (
    <section
      aria-labelledby="failed-deploy-title"
      className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3.5 gap-y-1 rounded-card border border-[color-mix(in_oklab,var(--err)_28%,var(--hairline))] bg-surface px-[18px] py-4 shadow-[var(--shadow-xs)] sm:grid-cols-[auto_minmax(0,1fr)_auto]"
    >
      <span className="row-span-3 hidden h-8 w-8 place-items-center rounded-control bg-err-soft text-err sm:grid">
        <Icon name="xCircle" size={17} />
      </span>
      <h2 id="failed-deploy-title" className="text-md font-semibold text-fg">
        Deploy #{d.number} failed{step && ` at the ${step} step`}
      </h2>
      <span className="self-center whitespace-nowrap text-xs text-fg3" title={formatStamp(when)}>
        {timeAgo(when)}
      </span>
      <p className="col-span-2 text-sm text-fg2 sm:col-start-2">{failureImpact(d, live, domains.data?.[0]?.hostname)}</p>
      {line && (
        <div className="col-span-2 mt-2 flex items-start gap-2.5 rounded-control border border-hairline bg-sunken px-3 py-2 sm:col-start-2">
          {d.exit_code != null && d.exit_code !== 0 && (
            <span className="whitespace-nowrap pt-px text-xs text-fg3">exit {d.exit_code}</span>
          )}
          <code className="whitespace-pre-wrap break-all font-mono text-xs text-fg">{line}</code>
        </div>
      )}
      <div className="col-span-2 mt-1.5 flex flex-wrap gap-2 sm:col-start-2">
        <Link to={`/projects/${project}/deployments?d=${d.number}`} className={btn("secondary", "sm")}>
          <Icon name="logs" size={15} /> Full log
        </Link>
        {commit && d.git_commit && (
          <a href={commit} target="_blank" rel="noreferrer" className={btn("secondary", "sm")}>
            <Icon name="external" size={15} /> {d.git_commit.slice(0, 7)} on GitHub
          </a>
        )}
        {canRetry && (
          <Button
            size="sm"
            variant="ghost"
            icon="refresh"
            title="Deploys again from the current files or branch, not only this commit"
            loading={deploy.isPending}
            onClick={() =>
              deploy.mutate(undefined, {
                onSuccess: (n) => toast.info(`Deployment #${n.number} started`),
                onError: (e) => toast.err("Could not start the deployment", errorText(e)),
              })
            }
          >
            Retry
          </Button>
        )}
      </div>
    </section>
  );
}
