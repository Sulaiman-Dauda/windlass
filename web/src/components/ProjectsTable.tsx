import { Link, useNavigate } from "react-router";
import type { Project } from "../api/projects";
import { useDeployments, useServices, summariseServices, deploymentTone, isActive } from "../api/deployments";
import { useDomains } from "../api/domains";
import { StatusPill, StatusDot } from "../ui/Badge";
import { Icon } from "../ui/Icon";
import { Skeleton } from "../ui/Skeleton";
import { timeAgo } from "../ui/format";
import { Monogram } from "./Layout";

/**
 * Live project list: status from compose ps, the newest deployment and the
 * first routed domain. Each row reads the same cache entries the project
 * screens use, so opening a project shows data that is already warm.
 */
export default function ProjectsTable({ projects, loading }: { projects: Project[]; loading?: boolean }) {
  return (
    <div className="overflow-x-auto rounded-card border border-hairline bg-surface shadow-[var(--shadow-xs)]">
      <table className="w-full min-w-[560px] table-fixed text-sm">
        <colgroup>
          <col />
          <col className="w-[140px]" />
          <col className="hidden w-[24%] md:table-column" />
          <col className="w-[150px]" />
          <col className="w-10" />
        </colgroup>
        <thead>
          <tr className="border-b border-hairline bg-surface2 text-left text-xs text-fg3">
            <th className="h-9 px-4 font-medium">Project</th>
            <th className="h-9 px-4 font-medium">Status</th>
            <th className="hidden h-9 px-4 font-medium md:table-cell">Domain</th>
            <th className="h-9 px-4 font-medium">Last deployment</th>
            <th className="h-9" aria-hidden="true" />
          </tr>
        </thead>
        <tbody className="divide-y divide-hairline">
          {loading &&
            [0, 1, 2].map((i) => (
              <tr key={i}>
                <td className="px-4 py-3.5" colSpan={5}>
                  <Skeleton className="h-5 w-1/3" />
                </td>
              </tr>
            ))}
          {projects.map((p) => (
            <ProjectRow key={p.name} project={p} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ProjectRow({ project }: { project: Project }) {
  const navigate = useNavigate();
  const services = useServices(project.name, 15_000);
  const deployments = useDeployments(project.name);
  const domains = useDomains(project.name, 30_000);

  const summary = summariseServices(services.data?.services);
  const latest = deployments.data?.[0];
  const domain = domains.data?.[0];
  const source = project.git_repo
    ? `${repoName(project.git_repo)}${project.git_branch ? ` · ${project.git_branch}` : ""}`
    : project.source === "template"
      ? "Template"
      : "Local files";

  return (
    <tr
      className="group cursor-pointer transition-colors duration-100 hover:bg-surface2"
      onClick={() => navigate(`/projects/${project.name}`)}
    >
      <td className="px-4 py-3">
        <div className="flex min-w-0 items-center gap-3">
          <Monogram name={project.name} size="md" />
          <div className="min-w-0">
            <Link
              to={`/projects/${project.name}`}
              onClick={(e) => e.stopPropagation()}
              className="block truncate font-semibold text-fg"
            >
              {project.name}
            </Link>
            <div className="flex min-w-0 items-center gap-1.5 text-xs text-fg3">
              {project.git_repo && <Icon name="gitBranch" size={13} />}
              <span className="truncate">{source}</span>
            </div>
          </div>
        </div>
      </td>
      <td className="px-4 py-3">
        {summary ? (
          <StatusPill tone={summary.tone} live={summary.tone === "ok"}>
            {summary.label}
            {summary.total > 0 && (
              <span className="font-medium tabular-nums opacity-75">
                {summary.running}/{summary.total}
              </span>
            )}
          </StatusPill>
        ) : (
          <Skeleton className="h-[22px] w-20 rounded-full" />
        )}
      </td>
      <td className="hidden px-4 py-3 md:table-cell">
        {domain ? (
          <a
            href={`https://${domain.hostname}`}
            target="_blank"
            rel="noreferrer"
            onClick={(e) => e.stopPropagation()}
            className="inline-flex max-w-full items-center gap-1.5 text-fg2 hover:text-accent"
          >
            <span className="truncate">{domain.hostname}</span>
            {domains.data && domains.data.length > 1 && (
              <span className="flex-none text-xs text-fg3">+{domains.data.length - 1}</span>
            )}
            <Icon name="external" size={13} className="text-fg3" />
          </a>
        ) : (
          <span className="text-fg3">No domain</span>
        )}
      </td>
      <td className="px-4 py-3">
        {latest ? (
          <div className="flex min-w-0 items-center gap-2">
            <StatusDot tone={deploymentTone(latest.status)} live={isActive(latest.status)} />
            <span className="truncate text-fg2">
              <span className="font-medium text-fg">#{latest.number}</span>{" "}
              {isActive(latest.status) ? latest.status : timeAgo(latest.finished_at || latest.created_at)}
            </span>
          </div>
        ) : deployments.data ? (
          <span className="text-fg3">Never deployed</span>
        ) : (
          <Skeleton className="h-4 w-24" />
        )}
      </td>
      <td className="pr-3 text-right">
        <Icon name="chevronRight" size={16} className="text-fg3 transition-transform duration-150 group-hover:translate-x-0.5" />
      </td>
    </tr>
  );
}

/** "https://github.com/acme/app.git" -> "acme/app". */
export function repoName(url: string): string {
  const m = url.match(/[:/]([^/:]+\/[^/]+?)(?:\.git)?\/?$/);
  return m ? m[1] : url;
}
