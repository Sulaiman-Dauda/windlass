import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";

interface Backup {
  id: number;
  kind: string;
  destination: string;
  size: number;
  status: string;
  error?: string;
  created_at: string;
}

interface Schedule {
  interval: string;
  destination: string;
  retention_count: number;
  enabled: boolean;
}

function fmtSize(bytes: number): string {
  if (bytes > 1 << 20) return (bytes / (1 << 20)).toFixed(1) + " MB";
  if (bytes > 1 << 10) return (bytes / (1 << 10)).toFixed(1) + " KB";
  return bytes + " B";
}

export default function BackupsTab({ project }: { project: string }) {
  const qc = useQueryClient();
  const key = ["projects", project, "backups"];
  const backups = useQuery<Backup[]>({
    queryKey: key,
    queryFn: () => api(`/projects/${project}/backups`),
  });

  const create = useMutation({
    mutationFn: () => api(`/projects/${project}/backups`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: key }),
  });
  const restore = useMutation({
    mutationFn: (id: number) =>
      api(`/projects/${project}/backups/${id}/restore`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["projects", project] }),
  });

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between">
        <p className="text-sm text-zinc-500">
          Backups archive the project directory (compose, env, configs — plus
          a database dump for template databases). Restore replaces the
          directory; deploy afterwards to apply it.
        </p>
        <button
          onClick={() => create.mutate()}
          disabled={create.isPending}
          className="ml-4 shrink-0 rounded-md bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
        >
          {create.isPending ? "Backing up…" : "Back up now"}
        </button>
      </div>
      {(create.isError || restore.isError) && (
        <p className="mt-2 text-sm text-red-400">
          {((create.error ?? restore.error) as Error)?.message ?? "Operation failed"}
        </p>
      )}
      {restore.isSuccess && (
        <p className="mt-2 text-sm text-emerald-400">
          Restored. Deploy the project to apply the restored files.
        </p>
      )}

      <div className="mt-4 space-y-2">
        {backups.data?.map((b) => (
          <div
            key={b.id}
            className="flex items-center justify-between rounded-md border border-zinc-900 bg-zinc-900/40 px-4 py-2.5 text-sm"
          >
            <div>
              <span className="font-mono text-xs text-zinc-500">#{b.id}</span>
              <span className="ml-3">{new Date(b.created_at).toLocaleString()}</span>
              <span className="ml-3 text-xs text-zinc-500">
                {b.kind} · {b.destination} · {fmtSize(b.size)}
              </span>
            </div>
            <div className="flex items-center gap-3">
              {b.status === "done" ? (
                <button
                  onClick={() => {
                    if (confirm(`Restore backup #${b.id}? Current project files are replaced.`)) {
                      restore.mutate(b.id);
                    }
                  }}
                  className="text-xs text-zinc-400 hover:text-zinc-100"
                >
                  Restore
                </button>
              ) : (
                <span className="text-xs text-red-400" title={b.error}>
                  {b.status}
                </span>
              )}
            </div>
          </div>
        ))}
        {backups.data?.length === 0 && (
          <p className="text-sm text-zinc-600">No backups yet.</p>
        )}
      </div>

      <ScheduleEditor project={project} />
    </div>
  );
}

function ScheduleEditor({ project }: { project: string }) {
  const qc = useQueryClient();
  const key = ["projects", project, "backup-schedule"];
  const schedule = useQuery<Schedule>({
    queryKey: key,
    queryFn: () => api(`/projects/${project}/backups/schedule`),
  });
  const [draft, setDraft] = useState<Schedule | null>(null);
  const current = draft ?? schedule.data ?? null;

  const save = useMutation({
    mutationFn: (s: Schedule) =>
      api(`/projects/${project}/backups/schedule`, {
        method: "PUT",
        body: JSON.stringify(s),
      }),
    onSuccess: () => {
      setDraft(null);
      qc.invalidateQueries({ queryKey: key });
    },
  });

  if (!current) return null;

  const update = (patch: Partial<Schedule>) =>
    setDraft({ ...current, ...patch });

  return (
    <div className="mt-8 rounded-md border border-zinc-900 p-4">
      <h3 className="text-sm font-medium">Scheduled backups</h3>
      <div className="mt-3 flex flex-wrap items-end gap-3">
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={current.enabled}
            onChange={(e) => update({ enabled: e.target.checked })}
          />
          Enabled
        </label>
        <label>
          <span className="mb-1 block text-xs text-zinc-500">Interval</span>
          <select
            value={current.interval}
            onChange={(e) => update({ interval: e.target.value })}
            className="rounded-md border border-zinc-800 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 outline-none"
          >
            <option value="hourly">Hourly</option>
            <option value="daily">Daily</option>
            <option value="weekly">Weekly</option>
          </select>
        </label>
        <label>
          <span className="mb-1 block text-xs text-zinc-500">Destination</span>
          <select
            value={current.destination}
            onChange={(e) => update({ destination: e.target.value })}
            className="rounded-md border border-zinc-800 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 outline-none"
          >
            <option value="local">Local</option>
            <option value="s3">S3</option>
          </select>
        </label>
        <label>
          <span className="mb-1 block text-xs text-zinc-500">Keep last</span>
          <input
            type="number"
            min={1}
            value={current.retention_count}
            onChange={(e) => update({ retention_count: parseInt(e.target.value, 10) || 7 })}
            className="w-20 rounded-md border border-zinc-800 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 outline-none"
          />
        </label>
        <button
          onClick={() => save.mutate(current)}
          disabled={save.isPending || draft === null}
          className="rounded-md border border-zinc-700 px-3 py-1 text-sm hover:bg-zinc-900 disabled:opacity-50"
        >
          Save
        </button>
      </div>
      {save.isError && (
        <p className="mt-2 text-sm text-red-400">
          {save.error instanceof Error ? save.error.message : "Save failed"}
        </p>
      )}
    </div>
  );
}
