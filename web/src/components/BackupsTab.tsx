import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { useCan } from "../api/auth";
import { Button } from "../ui/Button";
import { Card, CardHeader, CardFooter } from "../ui/Card";
import { Field, Input, Select, Switch } from "../ui/Field";
import { StatusPill } from "../ui/Badge";
import { Skeleton } from "../ui/Skeleton";
import { useConfirm } from "../ui/Modal";
import { useToast, errorText } from "../ui/Toast";
import { formatBytes, formatDateTime, timeAgo } from "../ui/format";

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

export default function BackupsTab({ project }: { project: string }) {
  const qc = useQueryClient();
  const can = useCan();
  const confirm = useConfirm();
  const toast = useToast();
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
    mutationFn: (id: number) => api(`/projects/${project}/backups/${id}/restore`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["projects", project] }),
  });

  const doRestore = async (b: Backup) => {
    const ok = await confirm({
      title: `Restore backup #${b.id}?`,
      body: `The project directory is replaced with the copy taken ${timeAgo(b.created_at)}. Running containers are not touched until you deploy.`,
      confirmLabel: "Restore files",
    });
    if (!ok) return;
    restore.mutate(b.id, {
      onSuccess: () => toast.ok(`Restored backup #${b.id}`, "Deploy the project to apply the restored files."),
      onError: (e) => toast.err("Restore failed", errorText(e)),
    });
  };

  return (
    <div className="max-w-5xl space-y-5">
      <Card className="overflow-hidden">
        <CardHeader
          title="Backups"
          description="Each backup archives the project directory (Compose files, .env, configs) plus a database dump for template databases."
          actions={
            can("member") && (
              <Button
                variant="primary"
                size="sm"
                icon="archive"
                loading={create.isPending}
                onClick={() =>
                  create.mutate(undefined, {
                    onSuccess: () => toast.ok("Backup complete"),
                    onError: (e) => toast.err("Backup failed", errorText(e)),
                  })
                }
              >
                Back up now
              </Button>
            )
          }
        />
        {backups.isLoading ? (
          <div className="p-5">
            <Skeleton className="h-5 w-1/2" />
          </div>
        ) : backups.data && backups.data.length > 0 ? (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[640px] text-sm">
              <thead>
                <tr className="border-b border-hairline bg-surface2 text-left text-xs text-fg3">
                  <th className="h-9 px-5 font-medium">Backup</th>
                  <th className="h-9 px-4 font-medium">Contents</th>
                  <th className="h-9 px-4 font-medium">Stored in</th>
                  <th className="h-9 px-4 text-right font-medium">Size</th>
                  <th className="h-9 px-4 font-medium">Status</th>
                  <th className="h-9 w-24" aria-hidden="true" />
                </tr>
              </thead>
              <tbody className="divide-y divide-hairline">
                {backups.data.map((b) => (
                  <tr key={b.id} className="transition-colors hover:bg-surface2">
                    <td className="px-5 py-3">
                      <div className="font-semibold text-fg">{formatDateTime(b.created_at)}</div>
                      <div className="text-xs text-fg3">
                        #{b.id} · {timeAgo(b.created_at)}
                      </div>
                    </td>
                    <td className="px-4 py-3 text-fg2">{b.kind}</td>
                    <td className="px-4 py-3 text-fg2">{b.destination === "s3" ? "S3" : "This server"}</td>
                    <td className="px-4 py-3 text-right tabular-nums text-fg2">{formatBytes(b.size)}</td>
                    <td className="px-4 py-3">
                      {b.status === "done" ? (
                        <StatusPill tone="ok">Complete</StatusPill>
                      ) : b.status === "failed" ? (
                        <span title={b.error}>
                          <StatusPill tone="err">Failed</StatusPill>
                        </span>
                      ) : (
                        <StatusPill tone="accent" busy>
                          {b.status}
                        </StatusPill>
                      )}
                    </td>
                    <td className="px-4 py-3 text-right">
                      {b.status === "done" && can("member") && (
                        <Button size="xs" icon="rollback" onClick={() => doRestore(b)} disabled={restore.isPending}>
                          Restore
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="px-5 py-8 text-center text-sm text-fg3">No backups yet.</p>
        )}
      </Card>

      <ScheduleEditor project={project} />
    </div>
  );
}

function ScheduleEditor({ project }: { project: string }) {
  const qc = useQueryClient();
  const can = useCan();
  const toast = useToast();
  const key = ["projects", project, "backup-schedule"];
  const schedule = useQuery<Schedule>({
    queryKey: key,
    queryFn: () => api(`/projects/${project}/backups/schedule`),
  });
  const [draft, setDraft] = useState<Schedule | null>(null);
  const current = draft ?? schedule.data ?? null;
  const editable = can("member");

  const save = useMutation({
    mutationFn: (s: Schedule) =>
      api(`/projects/${project}/backups/schedule`, {
        method: "PUT",
        body: JSON.stringify(s),
      }),
    onSuccess: () => {
      setDraft(null);
      toast.ok("Schedule saved");
      qc.invalidateQueries({ queryKey: key });
    },
    onError: (e) => toast.err("Could not save the schedule", errorText(e)),
  });

  if (!current) return null;

  const update = (patch: Partial<Schedule>) => setDraft({ ...current, ...patch });

  return (
    <Card>
      <CardHeader title="Schedule" description="Automatic backups, with older ones pruned past the retention count." />
      <div className="space-y-4 px-5 py-4">
        <Switch
          checked={current.enabled}
          onChange={(enabled) => update({ enabled })}
          disabled={!editable}
          label="Back up on a schedule"
        />
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Every" className="w-40">
            <Select
              value={current.interval}
              disabled={!editable || !current.enabled}
              onChange={(e) => update({ interval: e.target.value })}
            >
              <option value="hourly">Hour</option>
              <option value="daily">Day</option>
              <option value="weekly">Week</option>
            </Select>
          </Field>
          <Field label="Store in" className="w-44">
            <Select
              value={current.destination}
              disabled={!editable || !current.enabled}
              onChange={(e) => update({ destination: e.target.value })}
            >
              <option value="local">This server</option>
              <option value="s3">S3</option>
            </Select>
          </Field>
          <Field label="Keep the last" className="w-32">
            <Input
              type="number"
              min={1}
              disabled={!editable || !current.enabled}
              value={current.retention_count}
              onChange={(e) => update({ retention_count: parseInt(e.target.value, 10) || 7 })}
            />
          </Field>
        </div>
      </div>
      {editable && (
        <CardFooter note={draft ? "Unsaved changes." : undefined}>
          {draft && (
            <Button size="sm" variant="ghost" onClick={() => setDraft(null)}>
              Discard
            </Button>
          )}
          <Button size="sm" variant="primary" onClick={() => save.mutate(current)} disabled={draft === null} loading={save.isPending}>
            Save schedule
          </Button>
        </CardFooter>
      )}
    </Card>
  );
}
