import { useState, lazy, Suspense } from "react";
import { Link, Navigate, Route, Routes, useNavigate, useParams } from "react-router";
import { useCan } from "../api/auth";
import { useProject } from "../api/projects";
import {
  useCreateDeployment,
  useDeployments,
  useProjectAction,
  useServices,
  summariseServices,
  deploymentTone,
  isActive,
} from "../api/deployments";
import { useDomains } from "../api/domains";
import BackupsTab from "../components/BackupsTab";
import DeploymentsTab from "../components/DeploymentsTab";
import DomainsTab from "../components/DomainsTab";
import GitTab from "../components/GitTab";
import LogsTab from "../components/LogsTab";
import OverviewTab from "../components/OverviewTab";
import FilesTab from "../components/FilesTab";
import EnvTab from "../components/EnvTab";
import DeleteProjectDialog from "../components/DeleteProjectDialog";
import { repoName } from "../components/ProjectsTable";
import { Page, MetaItem, EmptyState } from "../ui/Page";
import { Button, IconButton } from "../ui/Button";
import { StatusPill, StatusDot } from "../ui/Badge";
import { Menu, MenuItem, MenuSeparator } from "../ui/Menu";
import { RouteTabs, type TabItem } from "../ui/Tabs";
import { Skeleton } from "../ui/Skeleton";
import { useConfirm } from "../ui/Modal";
import { useToast, errorText } from "../ui/Toast";
import { timeAgo } from "../ui/format";

const TerminalTab = lazy(() => import("../components/TerminalTab"));

// Segments under /projects/:name. The links are built absolute: inside a splat
// route React Router 7 resolves a relative link against the whole URL, so
// "deployments" clicked on /logs would land on /logs/deployments.
const TABS: (TabItem & { member?: boolean })[] = [
  { to: "", label: "Overview", end: true },
  { to: "deployments", label: "Deployments" },
  { to: "logs", label: "Logs" },
  { to: "terminal", label: "Terminal", member: true },
  { to: "domains", label: "Domains" },
  { to: "env", label: "Environment" },
  { to: "files", label: "Files" },
  { to: "git", label: "Git" },
  { to: "backups", label: "Backups" },
];

export default function ProjectDetail() {
  const { name = "", "*": rest = "" } = useParams();
  const project = useProject(name);
  const services = useServices(name);
  const deployments = useDeployments(name);
  const domains = useDomains(name);
  const deploy = useCreateDeployment(name);
  const action = useProjectAction(name);
  const navigate = useNavigate();
  const can = useCan();
  const confirm = useConfirm();
  const toast = useToast();
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  if (project.isError) {
    return (
      <Page title="Project not found" crumbs={[{ label: "Projects", to: "/projects" }, { label: name }]}>
        <EmptyState
          icon="projects"
          title={`There is no project called “${name}”`}
          desc="It may have been deleted, or its directory moved out of the stacks directory."
          actions={
            <Link to="/projects" className="text-sm font-semibold text-accent hover:underline">
              Back to projects
            </Link>
          }
        />
      </Page>
    );
  }

  const p = project.data;
  const summary = summariseServices(services.data?.services);
  const latest = deployments.data?.[0];
  const domain = domains.data?.[0];
  const current = TABS.find((t) => t.to && rest.split("/")[0] === t.to);

  const runDeploy = () =>
    deploy.mutate(undefined, {
      onSuccess: (d) => {
        toast.info(`Deployment #${d.number} started`, "Streaming its log now.");
        navigate(`/projects/${name}/deployments?d=${d.number}`);
      },
      onError: (e) => toast.err("Could not start the deployment", errorText(e)),
    });

  const runAction = async (a: "start" | "stop" | "restart") => {
    if (
      a === "stop" &&
      !(await confirm({
        title: `Stop ${name}?`,
        body: "Every service in this project stops until you start or deploy it again. Its domains will stop answering.",
        confirmLabel: "Stop services",
      }))
    )
      return;
    action.mutate(a, {
      onSuccess: () => toast.ok({ start: "Services started", stop: "Services stopped", restart: "Services restarted" }[a]),
      onError: (e) => toast.err(`Could not ${a} the project`, errorText(e)),
    });
  };

  return (
    <Page
      title={name}
      crumbs={[
        { label: "Projects", to: "/projects" },
        { label: name, to: current ? `/projects/${name}` : undefined },
        ...(current ? [{ label: current.label }] : []),
      ]}
      titleAdornment={
        summary ? (
          <StatusPill tone={summary.tone} live={summary.tone === "ok"}>
            {summary.label}
            {summary.total > 0 && (
              <span className="font-medium tabular-nums opacity-75">
                {summary.running}/{summary.total}
              </span>
            )}
          </StatusPill>
        ) : (
          <Skeleton className="h-[22px] w-24 rounded-full" />
        )
      }
      meta={
        p && (
          <>
            {p.git_repo ? (
              <MetaItem icon="gitBranch">
                {/^https?:\/\//.test(p.git_repo) ? (
                  <a href={p.git_repo.replace(/\.git$/, "")} target="_blank" rel="noreferrer" className="hover:text-accent">
                    {repoName(p.git_repo)}
                  </a>
                ) : (
                  repoName(p.git_repo)
                )}
                {p.git_branch && <span className="text-fg3"> on {p.git_branch}</span>}
                {p.auto_deploy && <span className="text-fg3"> · deploys on push</span>}
              </MetaItem>
            ) : (
              <MetaItem icon="file">{p.source === "template" ? "Created from a template" : "Compose files on disk"}</MetaItem>
            )}
            {domain && (
              <MetaItem icon="globe">
                <a href={`https://${domain.hostname}`} target="_blank" rel="noreferrer" className="hover:text-accent">
                  {domain.hostname}
                </a>
              </MetaItem>
            )}
            {latest ? (
              <span className="inline-flex items-center gap-2">
                <StatusDot tone={deploymentTone(latest.status)} live={isActive(latest.status)} />
                <Link to={`/projects/${name}/deployments?d=${latest.number}`} className="hover:text-accent">
                  {isActive(latest.status)
                    ? `Deploying (${latest.status})`
                    : `${latest.status === "succeeded" ? "Deployed" : `Deployment ${latest.status}`} ${timeAgo(latest.finished_at || latest.created_at)}`}
                </Link>
              </span>
            ) : (
              deployments.data && <MetaItem icon="deploy">Never deployed</MetaItem>
            )}
          </>
        )
      }
      actions={
        can("member") && (
          <>
            <Button variant="primary" icon="deploy" onClick={runDeploy} loading={deploy.isPending}>
              Deploy
            </Button>
            <Menu
              trigger={(props) => <IconButton {...props} icon="more" label="Project actions" variant="secondary" size="md" />}
            >
              <MenuItem icon="play" onSelect={() => runAction("start")}>
                Start services
              </MenuItem>
              <MenuItem icon="restart" onSelect={() => runAction("restart")}>
                Restart services
              </MenuItem>
              <MenuItem icon="stop" onSelect={() => runAction("stop")}>
                Stop services
              </MenuItem>
              <MenuSeparator />
              <MenuItem icon="trash" tone="danger" onSelect={() => setConfirmingDelete(true)}>
                Delete project
              </MenuItem>
            </Menu>
          </>
        )
      }
      tabs={
        <RouteTabs
          label="Project sections"
          items={TABS.filter((t) => !t.member || can("member")).map((t) => ({
            ...t,
            to: t.to ? `/projects/${name}/${t.to}` : `/projects/${name}`,
          }))}
        />
      }
    >
      {confirmingDelete && (
        <DeleteProjectDialog
          name={name}
          onClose={() => setConfirmingDelete(false)}
          onDeleted={() => {
            toast.ok(`Deleted ${name}`);
            navigate("/projects");
          }}
        />
      )}

      <Routes>
        <Route index element={<OverviewTab project={name} />} />
        <Route path="deployments" element={<DeploymentsTab project={name} />} />
        <Route path="domains" element={<DomainsTab project={name} />} />
        <Route path="git" element={<GitTab project={name} />} />
        <Route path="files" element={<FilesTab project={name} />} />
        <Route path="env" element={<EnvTab project={name} />} />
        <Route path="logs" element={<LogsTab project={name} />} />
        <Route
          path="terminal"
          element={
            can("member") ? (
              <Suspense fallback={<Skeleton className="h-[420px] w-full rounded-card" />}>
                <TerminalTab project={name} />
              </Suspense>
            ) : (
              <Navigate to={`/projects/${name}`} replace />
            )
          }
        />
        <Route path="backups" element={<BackupsTab project={name} />} />
      </Routes>
    </Page>
  );
}
