import type { ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { useCan } from "../api/auth";
import {
  PIPELINE,
  useDeploymentEvents,
  useDeployments,
  useRollback,
  deploymentTone,
  isActive,
  stageStates,
  type Deployment,
  type StageState,
} from "../api/deployments";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { StatusPill, StatusDot } from "../ui/Badge";
import { Callout, EmptyState } from "../ui/Page";
import { Icon } from "../ui/Icon";
import { Spinner } from "../ui/Spinner";
import { Skeleton } from "../ui/Skeleton";
import { CopyButton } from "../ui/Copy";
import { useConfirm } from "../ui/Modal";
import { useToast, errorText } from "../ui/Toast";
import { cn } from "../ui/cn";
import { elapsed, formatClock, formatDateTime, formatDuration, timeAgo } from "../ui/format";
import { TermPane } from "./TermPane";

export default function DeploymentsTab({ project }: { project: string }) {
  const deployments = useDeployments(project);
  const [params, setParams] = useSearchParams();
  const list = deployments.data ?? [];
  const requested = Number(params.get("d"));
  const selected = list.find((d) => d.number === requested) ?? list[0];

  // Keep the selection in the URL so a deployment's log can be linked to.
  const select = (n: number) => setParams({ d: String(n) }, { replace: true });

  if (deployments.isLoading) {
    return <Skeleton className="h-[420px] w-full rounded-card" />;
  }
  if (list.length === 0) {
    return (
      <EmptyState
        icon="deploy"
        title="No deployments yet"
        desc="Deploy renders the environment, validates the Compose file, pulls and builds images, starts the services and waits for them to be healthy. An interrupted deployment resumes where it stopped."
      />
    );
  }

  return (
    <div className="grid grid-cols-1 gap-5 lg:grid-cols-[300px_minmax(0,1fr)]">
      <Card className="h-fit overflow-hidden">
        <div className="border-b border-hairline px-4 py-3">
          <h3 className="text-sm font-semibold text-fg">History</h3>
        </div>
        <ul className="max-h-[70vh] divide-y divide-hairline overflow-y-auto">
          {list.map((d) => (
            <li key={d.id}>
              <button
                type="button"
                onClick={() => select(d.number)}
                aria-current={selected?.number === d.number}
                className={cn(
                  "relative block w-full px-4 py-2.5 text-left transition-colors",
                  selected?.number === d.number
                    ? "bg-accent-soft before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-accent"
                    : "hover:bg-surface2",
                )}
              >
                <div className="flex items-center gap-2">
                  {isActive(d.status) ? (
                    <Spinner size={12} className="text-accent" />
                  ) : (
                    <StatusDot tone={deploymentTone(d.status)} />
                  )}
                  <span className="text-sm font-semibold text-fg">#{d.number}</span>
                  <span className="text-sm capitalize text-fg2">{d.status}</span>
                  <span className="ml-auto text-xs text-fg3">{timeAgo(d.created_at)}</span>
                </div>
                <div className="mt-0.5 flex items-center gap-1.5 pl-5 text-xs text-fg3">
                  <span className="truncate">{d.triggered_by}</span>
                  {d.git_commit && <span className="font-mono">· {d.git_commit.slice(0, 7)}</span>}
                  <Duration d={d} className="ml-auto" />
                </div>
              </button>
            </li>
          ))}
        </ul>
      </Card>

      {selected && <DeploymentDetail key={selected.number} project={project} d={selected} latest={list[0].number} />}
    </div>
  );
}

function Duration({ d, className }: { d: Deployment; className?: string }) {
  const ms = elapsed(d.started_at || d.created_at, d.finished_at);
  if (ms === null || (isActive(d.status) && !d.started_at)) return null;
  return <span className={cn("tabular-nums", className)}>{formatDuration(ms)}</span>;
}

const lineStyle: Record<string, string> = {
  step: "text-term-accent font-medium",
  error: "text-term-err",
};

// The closing line carries the outcome as its message.
function doneStyle(message: string): string {
  if (message === "succeeded") return "text-term-ok font-medium";
  if (message === "failed") return "text-term-err font-medium";
  return "text-term-dim font-medium";
}

function DeploymentDetail({ project, d, latest }: { project: string; d: Deployment; latest: number }) {
  const { eventLog } = useDeploymentEvents(project, d.number);
  const rollback = useRollback(project);
  const can = useCan();
  const confirm = useConfirm();
  const toast = useToast();
  const [, setParams] = useSearchParams();
  const stages = stageStates(eventLog, d.status);
  const active = isActive(d.status);
  const canRollBack = can("member") && d.status === "succeeded" && d.number !== latest;

  const doRollback = async () => {
    const ok = await confirm({
      title: `Roll back to deployment #${d.number}?`,
      body: "This starts a new deployment that re-applies the images and files recorded for this one. Pull and build are skipped.",
      confirmLabel: "Roll back",
      tone: "primary",
    });
    if (!ok) return;
    rollback.mutate(d.number, {
      onSuccess: (r) => {
        toast.info(`Rollback started as #${r.number}`);
        setParams({ d: String(r.number) }, { replace: true });
      },
      onError: (e) => toast.err("Rollback failed to start", errorText(e)),
    });
  };

  return (
    <Card className="min-w-0 overflow-hidden">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-hairline px-5 py-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2.5">
            <h3 className="text-md font-semibold text-fg">Deployment #{d.number}</h3>
            <StatusPill tone={deploymentTone(d.status)} busy={active}>
              {d.status}
            </StatusPill>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-fg3">
            <span className="inline-flex items-center gap-1.5">
              <Icon name="user" size={13} /> {d.triggered_by}
            </span>
            {d.git_commit && (
              <span className="inline-flex items-center gap-1.5 font-mono">
                <Icon name="gitCommit" size={13} /> {d.git_commit.slice(0, 7)}
              </span>
            )}
            <span className="inline-flex items-center gap-1.5">
              <Icon name="clock" size={13} /> {formatDateTime(d.started_at || d.created_at)}
            </span>
            {!active && (
              <span className="inline-flex items-center gap-1.5">
                <Icon name="activity" size={13} /> <Duration d={d} />
              </span>
            )}
          </div>
        </div>
        <div className="flex items-center gap-1.5">
          {canRollBack && (
            <Button size="sm" icon="rollback" onClick={doRollback} loading={rollback.isPending}>
              Roll back to this
            </Button>
          )}
          <CopyButton
            size="sm"
            label="Copy log"
            value={eventLog.map((e) => `${formatClock(e.ts)}  ${e.message}`).join("\n")}
          />
        </div>
      </div>

      <ol className="flex items-center gap-1 overflow-x-auto border-b border-hairline bg-surface2 px-5 py-2.5 [scrollbar-width:none]">
        {PIPELINE.map((s, i) => (
          <li key={s.key} className="flex flex-none items-center gap-1">
            <Stage state={stages[s.key]} label={s.label} />
            {i < PIPELINE.length - 1 && <span aria-hidden="true" className="mx-1.5 h-px w-5 bg-edge" />}
          </li>
        ))}
      </ol>

      {d.status === "failed" && d.error && (
        <Callout tone="err" title="Deployment failed" className="mx-5 mt-4">
          <span className="break-words font-mono text-xs">{d.error}</span>
        </Callout>
      )}

      <TermPane tick={eventLog.length} className={cn("max-h-[62vh] min-h-[320px]", d.status === "failed" && d.error && "mt-4")}>
        {eventLog.map((ev) => (
          <div key={ev.seq} className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4">
            <span className="select-none text-term-dim">{formatClock(ev.ts)}</span>
            <span className={ev.type === "done" ? doneStyle(ev.message) : (lineStyle[ev.type] ?? "")}>{ev.message}</span>
          </div>
        ))}
        {eventLog.length === 0 && (
          <span className="text-term-dim">{active ? "Waiting for output…" : "Loading log…"}</span>
        )}
      </TermPane>
    </Card>
  );
}

const stageIcon: Record<StageState, { className: string; node: ReactNode }> = {
  done: { className: "bg-ok-soft text-ok", node: <Icon name="check" size={12} /> },
  active: { className: "bg-accent-soft text-accent", node: <Spinner size={10} /> },
  failed: { className: "bg-err-soft text-err", node: <Icon name="x" size={11} /> },
  skipped: { className: "border border-dashed border-edge text-fg3", node: <Icon name="minus" size={11} /> },
  pending: { className: "border border-edge text-fg3", node: null },
};

function Stage({ state, label }: { state: StageState; label: string }) {
  const s = stageIcon[state];
  return (
    <span className="inline-flex items-center gap-1.5" title={`${label}: ${state}`}>
      <span className={cn("grid h-[18px] w-[18px] place-items-center rounded-full", s.className)}>{s.node}</span>
      <span
        className={cn(
          "text-xs font-semibold",
          state === "active" ? "text-accent" : state === "failed" ? "text-err" : state === "done" ? "text-fg" : "text-fg3",
          state === "skipped" && "line-through decoration-fg3/50",
        )}
      >
        {label}
      </span>
    </span>
  );
}
