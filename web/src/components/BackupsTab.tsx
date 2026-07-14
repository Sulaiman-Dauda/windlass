import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Field, Input, Select } from "../ui/Field";
import { Icon } from "../ui/Icon";

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
      <div className="flex items-start justify-between gap-4">
        <p className="text-sm leading-relaxed text-fg2">
          Backups archive the project directory (compose, env, configs — plus
          a database dump for template databases). Restore replaces the
          directory; deploy afterwards to apply it.
        </p>
        <Button
          variant="primary"
          onClick={() => create.mutate()}
          disabled={create.isPending}
          className="shrink-0"
        >
          <Icon name="download" size={15} />
          {create.isPending ? "Backing up…" : "Back up now"}
        </Button>
      </div>
      {(create.isError || restore.isError) && (
        <p className="mt-2 text-sm text-err">
          {((create.error ?? restore.error) as Error)?.message ?? "Operation failed"}
        </p>
      )}
      {restore.isSuccess && (
        <p className="mt-2 text-sm text-ok">
          Restored. Deploy the project to apply the restored files.
        </p>
      )}

      <div className="mt-4 space-y-2">
        {backups.data?.map((b) => (
          <div
            key={b.id}
            className="flex items-center justify-between rounded-[10px] border border-hairline bg-surface2 px-4 py-3 text-sm"
          >
            <div>
              <span className="font-mono text-xs text-fg3">#{b.id}</span>
              <span className="ml-3 text-fg">{new Date(b.created_at).toLocaleString()}</span>
              <span className="ml-3 text-xs text-fg3">
                {b.kind} · {b.destination} · {fmtSize(b.size)}
              </span>
            </div>
            <div className="flex items-center gap-3">
              {b.status === "done" ? (
                <Button
                  size="sm"
                  onClick={() => {
                    if (confirm(`Restore backup #${b.id}? Current project files are replaced.`)) {
                      restore.mutate(b.id);
                    }
                  }}
                >
                  Restore
                </Button>
              ) : (
                <span className="text-xs text-err" title={b.error}>
                  {b.status}
                </span>
              )}
            </div>
          </div>
        ))}
        {backups.data?.length === 0 && (
          <p className="text-sm text-fg3">No backups yet.</p>
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
    <Card className="mt-8 p-4">
      <h3 className="text-sm font-semibold text-fg">Scheduled backups</h3>
      <div className="mt-3 flex flex-wrap items-end gap-3">
        <label className="flex items-center gap-2.5 py-2.5 text-sm text-fg">
          <input
            type="checkbox"
            checked={current.enabled}
            onChange={(e) => update({ enabled: e.target.checked })}
            className="h-[18px] w-[18px] flex-none rounded-[6px] accent-[var(--color-accent-fill)]"
          />
          Enabled
        </label>
        <Field label="Interval">
          <Select
            value={current.interval}
            onChange={(e) => update({ interval: e.target.value })}
          >
            <option value="hourly">Hourly</option>
            <option value="daily">Daily</option>
            <option value="weekly">Weekly</option>
          </Select>
        </Field>
        <Field label="Destination">
          <Select
            value={current.destination}
            onChange={(e) => update({ destination: e.target.value })}
          >
            <option value="local">Local</option>
            <option value="s3">S3</option>
          </Select>
        </Field>
        <Field label="Keep last" className="w-24">
          <Input
            type="number"
            min={1}
            value={current.retention_count}
            onChange={(e) => update({ retention_count: parseInt(e.target.value, 10) || 7 })}
          />
        </Field>
        <Button
          onClick={() => save.mutate(current)}
          disabled={save.isPending || draft === null}
        >
          Save
        </Button>
      </div>
      {save.isError && (
        <p className="mt-2 text-sm text-err">
          {save.error instanceof Error ? save.error.message : "Save failed"}
        </p>
      )}
    </Card>
  );
}
