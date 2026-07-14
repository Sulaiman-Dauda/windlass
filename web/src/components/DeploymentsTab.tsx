import { useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import {
  TERMINAL_STATUSES,
  useCreateDeployment,
  useDeploymentEvents,
  useDeployments,
  type Deployment,
} from "../api/deployments";
import { Button } from "../ui/Button";
import { StatusPill, type Tone } from "../ui/Badge";
import { Icon } from "../ui/Icon";
import { cn } from "../ui/cn";

const statusTones: Record<string, Tone> = {
  succeeded: "ok",
  failed: "err",
  cancelled: "idle",
};

function StatusBadge({ status }: { status: string }) {
  const tone = statusTones[status] ?? "warn";
  const active = !TERMINAL_STATUSES.includes(status);
  return (
    <StatusPill tone={tone} live={active}>
      {status}
    </StatusPill>
  );
}

export default function DeploymentsTab({ project }: { project: string }) {
  const deployments = useDeployments(project);
  const create = useCreateDeployment(project);
  const [selected, setSelected] = useState<number | null>(null);
  const qc = useQueryClient();
  const rollback = useMutation({
    mutationFn: (number: number) =>
      api<Deployment>(`/projects/${project}/deployments/${number}/rollback`, {
        method: "POST",
      }),
    onSuccess: (d) => {
      setSelected(d.number);
      qc.invalidateQueries({ queryKey: ["projects", project, "deployments"] });
    },
  });

  // Auto-select the newest deployment (e.g. right after clicking Deploy).
  useEffect(() => {
    if (selected === null && deployments.data?.length) {
      setSelected(deployments.data[0].number);
    }
  }, [deployments.data, selected]);

  return (
    <div>
      <div className="flex items-center justify-between gap-4">
        <p className="text-sm leading-relaxed text-fg2">
          Deployments run: env render → validate → sync → pull → build → up →
          verify. Interrupted deployments resume automatically.
        </p>
        <Button
          variant="primary"
          onClick={() =>
            create.mutate(undefined, {
              onSuccess: (d) => setSelected(d.number),
            })
          }
          disabled={create.isPending}
        >
          <Icon name="deploy" size={15} />
          Deploy
        </Button>
      </div>
      {create.isError && (
        <p className="mt-2 text-sm text-err">
          {create.error instanceof Error ? create.error.message : "Deploy failed"}
        </p>
      )}

      <div className="mt-4 flex gap-6">
        <div className="w-64 shrink-0 space-y-1">
          {deployments.data?.map((d: Deployment) => (
            <button
              key={d.id}
              onClick={() => setSelected(d.number)}
              className={cn(
                "block w-full rounded-[10px] border px-4 py-3 text-left transition-colors",
                selected === d.number
                  ? "border-accent bg-surface2"
                  : "border-hairline bg-surface2 hover:border-edge",
              )}
            >
              <div className="flex items-center justify-between">
                <span className="text-sm text-fg">#{d.number}</span>
                <StatusBadge status={d.status} />
              </div>
              <div className="mt-0.5 flex items-center justify-between text-xs text-fg3">
                <span>
                  {d.triggered_by}
                  {d.git_commit ? ` · ${d.git_commit.slice(0, 7)}` : ""}
                </span>
                {d.status === "succeeded" &&
                  deployments.data &&
                  deployments.data[0].number !== d.number && (
                    <span
                      role="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        if (confirm(`Roll back to deployment #${d.number}?`)) {
                          rollback.mutate(d.number);
                        }
                      }}
                      className="text-fg3 hover:text-fg"
                    >
                      roll back
                    </span>
                  )}
              </div>
            </button>
          ))}
          {deployments.data?.length === 0 && (
            <p className="text-sm text-fg3">No deployments yet.</p>
          )}
        </div>

        <div className="min-w-0 flex-1">
          {selected !== null && (
            <DeploymentLog project={project} number={selected} />
          )}
        </div>
      </div>
    </div>
  );
}

const eventStyles: Record<string, string> = {
  step: "text-accent",
  error: "text-err",
  done: "text-ok",
};

function DeploymentLog({ project, number }: { project: string; number: number }) {
  const { eventLog } = useDeploymentEvents(project, number);
  const pane = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = pane.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [eventLog.length]);

  return (
    <div
      ref={pane}
      className="max-h-[26rem] overflow-auto rounded-[13px] border border-hairline bg-term p-4 font-mono text-xs leading-relaxed"
    >
      {eventLog.map((ev) => (
        <div key={ev.seq} className={eventStyles[ev.type] ?? "text-fg2"}>
          {ev.type === "step" ? "── " : ""}
          {ev.message}
        </div>
      ))}
      {eventLog.length === 0 && (
        <span className="text-fg3">Waiting for output…</span>
      )}
    </div>
  );
}
