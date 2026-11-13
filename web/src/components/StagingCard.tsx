import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { useCan } from "../api/auth";
import { useProject } from "../api/projects";
import { deploymentTone, isActive, useCreateDeployment } from "../api/deployments";
import {
  shortCommit,
  useCopyToLive,
  usePromote,
  useSetAutoPromote,
  useStagingDiff,
  useStagingStatus,
  type DeploymentRef,
  type GitCommit,
  type StagingStatus,
} from "../api/staging";
import { Card, CardHeader, CardFooter } from "../ui/Card";
import { StatusDot, Tag } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Switch } from "../ui/Field";
import { Modal, useConfirm } from "../ui/Modal";
import { Skeleton } from "../ui/Skeleton";
import { useToast, errorText } from "../ui/Toast";
import { diffLines } from "../ui/diff";
import { cn } from "../ui/cn";
import { plural, timeAgo } from "../ui/format";
import RecreateStagingDialog from "./RecreateStagingDialog";

/**
 * The live and staging pair with Promote, on either project's overview.
 * Renders nothing for a project without a staging.
 */
export default function StagingCard({ project }: { project: string }) {
  const p = useProject(project).data;
  const status = useStagingStatus(project, !!(p?.staging || p?.staging_of));
  const [recreating, setRecreating] = useState<StagingStatus | null>(null);
  return (
    <>
      {p && (p.staging || p.staging_of) && (
        status.isLoading ? <Skeleton className="h-[188px] w-full rounded-card" /> : status.data && (
          <PairCard st={status.data} onRecreate={project === status.data.staging ? () => setRecreating(status.data!) : undefined} />
        )
      )}
      {/* Status can briefly be absent between deletion and creation. Keep the dialog mounted. */}
      {recreating && <RecreateStagingDialog st={recreating} onClose={() => setRecreating(null)} />}
    </>
  );
}

function Side({
  label,
  name,
  current,
  latest,
  currentLabel,
}: {
  label: string;
  name: string;
  current: DeploymentRef | null;
  latest: DeploymentRef | null;
  currentLabel: string;
}) {
  const busy = latest && isActive(latest.status) ? latest : null;
  const failed = latest && latest.status === "failed" && latest.number !== current?.number ? latest : null;
  return (
    <div className="min-w-0 px-5 py-3.5">
      <div className="flex items-center gap-2 text-xs text-fg3">
        <span className="font-semibold uppercase tracking-[0.04em]">{label}</span>
        <span className="truncate font-mono">{name}</span>
      </div>
      {current ? (
        <Link
          to={`/projects/${name}/deployments?d=${current.number}`}
          className="group mt-1 flex min-h-7 items-baseline gap-2 py-0.5"
        >
          <span className="text-md font-semibold text-fg group-hover:text-accent">#{current.number}</span>
          {current.commit && <span className="font-mono text-xs text-fg2">{shortCommit(current.commit)}</span>}
          <span className="text-xs text-fg3">{currentLabel}</span>
        </Link>
      ) : (
        <p className="mt-1.5 text-sm text-fg3">No succeeded deployment yet</p>
      )}
      {(busy || failed) && (
        <Link
          to={`/projects/${name}/deployments?d=${(busy ?? failed)!.number}`}
          className="inline-flex min-h-6 items-center gap-1.5 text-xs text-fg2 hover:text-accent"
        >
          <StatusDot tone={deploymentTone((busy ?? failed)!.status)} live={!!busy} />
          {busy ? `#${busy.number} is ${busy.status}` : `#${failed!.number} failed`}
        </Link>
      )}
    </div>
  );
}

const driftLabel = {
  changed: "Changed on staging",
  staging_only: "Only on staging",
  live_only: "Only on live",
} as const;

/** One line per commit: hash, subject, and on the card the author and when. */
function CommitList({ commits, compact }: { commits: GitCommit[]; compact?: boolean }) {
  return (
    <ul className="divide-y divide-hairline">
      {commits.map((c) => (
        <li key={c.hash} className={cn("flex items-baseline gap-3 py-1.5 text-xs", compact ? "px-3" : "px-5")}>
          <span className="shrink-0 font-mono text-fg2">{shortCommit(c.hash)}</span>
          <span className="min-w-0 flex-1 truncate text-fg" title={c.subject}>
            {c.subject}
          </span>
          {!compact && (
            <span className="hidden shrink-0 text-fg3 sm:inline">
              {c.author}, {timeAgo(c.time)}
            </span>
          )}
        </li>
      ))}
    </ul>
  );
}

function commitsHeading(st: StagingStatus) {
  const n = st.commits?.length ?? 0;
  return st.commits_more ? `More than ${plural(n, "commit")} to promote` : `${plural(n, "commit")} to promote`;
}

function envMissingText(st: StagingStatus) {
  const keys = st.env_missing ?? [];
  return (
    <>
      {st.live}'s .env lacks {keys.length === 1 ? "a key" : `${keys.length} keys`} staging has:{" "}
      <span className="font-mono">{keys.join(", ")}</span>. Add {keys.length === 1 ? "it" : "them"} to {st.live} first unless
      its compose file or an env_file sets {keys.length === 1 ? "it" : "them"}.
    </>
  );
}

/** Staging's branch has moved on past what staging last deployed. */
function behind(st: StagingStatus) {
  const last = st.staging_latest;
  return !!st.branch_head && !!last?.commit && !isActive(last.status) && st.branch_head !== last.commit;
}

/**
 * Promote with its confirm, then a jump to live's new deployment. Shared by
 * the pair card and staging's deployment detail.
 */
export function usePromoteAction(st: StagingStatus | null | undefined) {
  const confirm = useConfirm();
  const toast = useToast();
  const navigate = useNavigate();
  const promote = usePromote(st?.staging ?? "", st?.live ?? "");

  const run = async () => {
    const p = st?.promotable;
    if (!st || !p) return;
    const ok = await confirm({
      title: `Promote staging #${p.number} to live?`,
      body: (
        <>
          {st.live} deploys {p.commit ? <>commit <span className="font-mono">{shortCommit(p.commit)}</span> with </> : null}
          the images {st.staging} ran. Pull and build are skipped. {st.live} keeps its own compose files, environment and data.
          {st.commits && st.commits.length > 0 && (
            <span className="mt-3 block">
              <span className="block text-xs font-semibold text-fg2">{commitsHeading(st)}</span>
              <span className="mt-1 block max-h-56 overflow-auto rounded-control border border-hairline">
                <CommitList commits={st.commits} compact />
              </span>
            </span>
          )}
          {!!st.env_missing?.length && <span className="mt-2 block text-warn">{envMissingText(st)}</span>}
          {st.drift.length > 0 && (
            <span className="mt-2 block text-warn">
              {plural(st.drift.length, "compose file")} {st.drift.length === 1 ? "differs" : "differ"} from staging. Copy any
              change {st.live} needs first.
            </span>
          )}
        </>
      ),
      confirmLabel: "Promote to live",
      tone: "primary",
    });
    if (!ok) return;
    promote.mutate(p.number, {
      onSuccess: (d) => {
        toast.info(`Promoting as live #${d.number}`, "Streaming its log now.");
        navigate(`/projects/${st.live}/deployments?d=${d.number}`);
      },
      onError: (e) => toast.err("Could not promote", errorText(e)),
    });
  };
  return { run, pending: promote.isPending };
}

function PairCard({ st, onRecreate }: { st: StagingStatus; onRecreate?: () => void }) {
  const can = useCan();
  const toast = useToast();
  const promote = usePromoteAction(st);
  const setAuto = useSetAutoPromote(st.staging);
  const [diffing, setDiffing] = useState<StagingStatus["drift"][number] | null>(null);
  const [allCommits, setAllCommits] = useState(false);
  const deploy = useCreateDeployment(st.staging);
  const navigate = useNavigate();
  const member = can("member");
  const p = st.promotable;
  const commits = st.commits ?? [];
  const shown = allCommits ? commits : commits.slice(0, 5);

  return (
    <Card>
      <CardHeader
        title="Staging"
        description={
          <>
            {st.branch ? (
              <>
                Pushes to <span className="font-mono text-xs text-fg2">{st.branch}</span> deploy to {st.staging}. Promote deploys
                the commit and images staging verified to {st.live}.
              </>
            ) : (
              <>
                Try changes on {st.staging} first. Promote deploys the images staging verified to {st.live}.
              </>
            )}
          </>
        }
        actions={
          member && (
            st.can_recreate && onRecreate ? (
              <Button variant="danger" size="sm" onClick={onRecreate}>Recreate staging</Button>
            ) : (
              <Button
                variant="primary"
                size="sm"
                icon="deploy"
                onClick={promote.run}
                disabled={!!st.promote_blocked}
                loading={promote.pending}
              >
                {p ? `Promote #${p.number}` : "Promote"}
              </Button>
            )
          )
        }
      />
      <div className="grid grid-cols-1 divide-y divide-hairline sm:grid-cols-2 sm:divide-x sm:divide-y-0">
        <Side label="Live" name={st.live} current={st.live_current} latest={st.live_latest} currentLabel="running" />
        <Side label="Staging" name={st.staging} current={p} latest={st.staging_latest} currentLabel="verified" />
      </div>
      {st.promote_blocked && (
        <p className="border-t border-hairline px-5 py-2.5 text-xs text-fg3">
          Promote is unavailable because {st.promote_blocked}.
        </p>
      )}

      {behind(st) && (
        <div className="flex items-center gap-3 border-t border-hairline px-5 py-2.5 text-xs">
          <p className="min-w-0 flex-1 text-warn">
            Staging is behind <span className="font-mono">{st.branch}</span>: commit{" "}
            <span className="font-mono">{shortCommit(st.branch_head!)}</span> has not been deployed to {st.staging}.
          </p>
          {member && (
            <Button
              size="xs"
              variant="secondary"
              icon="deploy"
              loading={deploy.isPending}
              onClick={() =>
                deploy.mutate(undefined, {
                  onSuccess: (d) => navigate(`/projects/${st.staging}/deployments?d=${d.number}`),
                  onError: (e) => toast.err("Could not deploy staging", errorText(e)),
                })
              }
            >
              Deploy staging
            </Button>
          )}
        </div>
      )}

      {commits.length > 0 && (
        <div className="border-t border-hairline pb-1.5">
          <div className="px-5 pb-1 pt-3 text-xs font-semibold text-fg2">{commitsHeading(st)}</div>
          <CommitList commits={shown} />
          {commits.length > shown.length && (
            <div className="px-5 pt-1">
              <Button size="xs" variant="ghost" onClick={() => setAllCommits(true)}>
                Show all {commits.length}
              </Button>
            </div>
          )}
        </div>
      )}

      {!!st.env_missing?.length && (
        <p className="border-t border-hairline px-5 py-2.5 text-xs text-warn">{envMissingText(st)}</p>
      )}

      {st.drift.length > 0 && (
        <div className="border-t border-hairline">
          <div className="px-5 pb-1 pt-3 text-xs font-semibold text-fg2">
            Compose files that differ, apart from the staging hostnames
          </div>
          <ul className="divide-y divide-hairline">
            {st.drift.map((f) => (
              <li key={f.file} className="flex items-center gap-3 px-5 py-2">
                <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg">{f.file}</span>
                <span className="text-xs text-fg3">{driftLabel[f.state]}</span>
                {member && (
                  <Button size="xs" variant="ghost" onClick={() => setDiffing(f)}>
                    View diff
                  </Button>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}

      <CardFooter
        note={
          member ? (
            <Switch
              checked={st.auto_promote}
              disabled={setAuto.isPending}
              onChange={(next) =>
                setAuto.mutate(next, {
                  onSuccess: () => toast.ok(next ? "Auto-promote is on" : "Auto-promote is off"),
                  onError: (e) => toast.err("Could not change auto-promote", errorText(e)),
                })
              }
              label="Promote automatically"
              description="After each staging deployment succeeds, live deploys the same commit and images."
            />
          ) : st.auto_promote ? (
            "Each succeeded staging deployment is promoted to live automatically."
          ) : (
            "Live changes when a member promotes a staging deployment."
          )
        }
      />
      {diffing && <DiffDialog st={st} file={diffing.file} state={diffing.state} onClose={() => setDiffing(null)} />}
    </Card>
  );
}

function DiffDialog({
  st,
  file,
  state,
  onClose,
}: {
  st: StagingStatus;
  file: string;
  state: StagingStatus["drift"][number]["state"];
  onClose: () => void;
}) {
  const diff = useStagingDiff(st.live, file);
  const copy = useCopyToLive(st.live);
  const toast = useToast();
  const lines = diff.data ? diffLines(diff.data.live, diff.data.for_live) : [];
  const changed = lines.some((l) => l.kind !== "same");

  return (
    <Modal
      onClose={onClose}
      width={820}
      title={
        <span className="inline-flex items-center gap-2">
          <span className="font-mono">{file}</span>
          <Tag>{driftLabel[state]}</Tag>
        </span>
      }
      description={
        <>
          Lines marked + are what Copy to live adds to {st.live}'s file, and lines marked - are what it removes. Staging's
          hostnames are already put back to {st.live}'s. {st.live} uses the file from its next deployment or promotion.
        </>
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Close
          </Button>
          {state !== "live_only" && (
            <Button
              variant="primary"
              disabled={!diff.data || !changed}
              loading={copy.isPending}
              onClick={() =>
                copy.mutate(
                  { file, content: diff.data!.for_live },
                  {
                    onSuccess: () => {
                      toast.ok(`${file} copied to ${st.live}`, "Live uses it from its next deployment or promotion.");
                      onClose();
                    },
                    onError: (e) => toast.err("Could not copy the file", errorText(e)),
                  },
                )
              }
            >
              Copy to live
            </Button>
          )}
        </>
      }
    >
      {diff.isLoading ? (
        <Skeleton className="h-48 w-full" />
      ) : diff.isError ? (
        <p className="text-sm text-err">{errorText(diff.error)}</p>
      ) : (
        <div className="max-h-[56vh] overflow-auto rounded-control border border-hairline bg-surface2 py-2 font-mono text-xs leading-5">
          {lines.map((l, i) => (
            <div
              key={i}
              className={cn(
                "grid grid-cols-[1.5rem_minmax(0,1fr)] whitespace-pre px-2",
                l.kind === "add" && "bg-ok-soft text-fg",
                l.kind === "del" && "bg-err-soft text-fg",
                l.kind === "same" && "text-fg3",
              )}
            >
              <span aria-hidden="true" className={cn("select-none", l.kind === "add" ? "text-ok" : l.kind === "del" ? "text-err" : "")}>
                {l.kind === "add" ? "+" : l.kind === "del" ? "-" : " "}
              </span>
              <span>
                <span className="sr-only">{l.kind === "add" ? "Added: " : l.kind === "del" ? "Removed: " : ""}</span>
                {l.text || " "}
              </span>
            </div>
          ))}
          {!changed && <p className="px-3 py-2 text-fg3">The files match once the hostnames are put back.</p>}
        </div>
      )}
    </Modal>
  );
}
