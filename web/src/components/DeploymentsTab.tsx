import { useEffect, useRef, useState } from "react";
import {
  TERMINAL_STATUSES,
  useCreateDeployment,
  useDeploymentEvents,
  useDeployments,
  type Deployment,
} from "../api/deployments";

const statusColors: Record<string, string> = {
  succeeded: "text-emerald-400",
  failed: "text-red-400",
  cancelled: "text-zinc-500",
};

function StatusBadge({ status }: { status: string }) {
  const color = statusColors[status] ?? "text-amber-400";
  const active = !TERMINAL_STATUSES.includes(status);
  return (
    <span className={`inline-flex items-center gap-1.5 text-xs ${color}`}>
      {active && (
        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-current" />
      )}
      {status}
    </span>
  );
}

export default function DeploymentsTab({ project }: { project: string }) {
  const deployments = useDeployments(project);
  const create = useCreateDeployment(project);
  const [selected, setSelected] = useState<number | null>(null);

  // Auto-select the newest deployment (e.g. right after clicking Deploy).
  useEffect(() => {
    if (selected === null && deployments.data?.length) {
      setSelected(deployments.data[0].number);
    }
  }, [deployments.data, selected]);

  return (
    <div>
      <div className="flex items-center justify-between">
        <p className="text-sm text-zinc-500">
          Deployments run: env render → validate → sync → pull → build → up →
          verify. Interrupted deployments resume automatically.
        </p>
        <button
          onClick={() =>
            create.mutate(undefined, {
              onSuccess: (d) => setSelected(d.number),
            })
          }
          disabled={create.isPending}
          className="rounded-md bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
        >
          Deploy
        </button>
      </div>
      {create.isError && (
        <p className="mt-2 text-sm text-red-400">
          {create.error instanceof Error ? create.error.message : "Deploy failed"}
        </p>
      )}

      <div className="mt-4 flex gap-6">
        <div className="w-64 shrink-0 space-y-1">
          {deployments.data?.map((d: Deployment) => (
            <button
              key={d.id}
              onClick={() => setSelected(d.number)}
              className={`block w-full rounded-md border px-3 py-2 text-left ${
                selected === d.number
                  ? "border-zinc-700 bg-zinc-900"
                  : "border-transparent hover:bg-zinc-900/60"
              }`}
            >
              <div className="flex items-center justify-between">
                <span className="text-sm">#{d.number}</span>
                <StatusBadge status={d.status} />
              </div>
              <div className="mt-0.5 text-xs text-zinc-600">
                {d.triggered_by}
                {d.git_commit ? ` · ${d.git_commit.slice(0, 7)}` : ""}
              </div>
            </button>
          ))}
          {deployments.data?.length === 0 && (
            <p className="text-sm text-zinc-600">No deployments yet.</p>
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
  step: "text-sky-300",
  error: "text-red-400",
  done: "text-emerald-400",
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
      className="h-96 overflow-auto rounded-md border border-zinc-900 bg-black p-3 font-mono text-xs leading-5"
    >
      {eventLog.map((ev) => (
        <div key={ev.seq} className={eventStyles[ev.type] ?? "text-zinc-300"}>
          {ev.type === "step" ? "── " : ""}
          {ev.message}
        </div>
      ))}
      {eventLog.length === 0 && (
        <span className="text-zinc-600">Waiting for output…</span>
      )}
    </div>
  );
}
