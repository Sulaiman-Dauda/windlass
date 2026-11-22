import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { useProjects } from "../api/projects";
import { Card, CardHeader, CardFooter } from "../ui/Card";
import { Button, IconButton } from "../ui/Button";
import { Input, Select, Field, Switch } from "../ui/Field";
import { StatusPill, Tag } from "../ui/Badge";
import { FormError } from "../ui/Page";
import { Modal, useConfirm } from "../ui/Modal";
import { useToast, errorText } from "../ui/Toast";
import { timeAgo } from "../ui/format";

type ChannelKind = "webhook" | "ntfy" | "discord" | "slack" | "email";

export interface AlertChannel {
  id: number;
  name: string;
  kind: ChannelKind;
  enabled: boolean;
  events: string[];
  project?: string;
  config: Record<string, string | boolean>;
}

interface Delivery {
  id: number;
  channel: string;
  event: string;
  project?: string;
  status: "pending" | "sent" | "failed";
  attempts: number;
  last_error?: string;
  created_at: string;
}

const KIND_LABEL: Record<ChannelKind, string> = {
  webhook: "Webhook",
  ntfy: "ntfy",
  discord: "Discord",
  slack: "Slack",
  email: "Email",
};

const EVENT_LABEL: Record<string, string> = {
  deploy_failed: "Deploy failed",
  deploy_succeeded: "Deploy succeeded",
  container_died: "Container died",
  restart_loop: "Restart loop",
  unhealthy: "Container unhealthy",
  cert_expiring: "Certificate expiring",
  disk: "Disk filling up",
  no_backup: "No recent backup",
  proxy: "Proxy problem",
  update: "Update available",
  drift: "Files changed since deploy",
  dns: "Domain DNS problem",
  caddy_resume: "Caddy routes need restoring",
  binary_owner: "Binary owner problem",
};

interface FieldSpec {
  key: string;
  label: string;
  placeholder?: string;
  secret?: boolean;
  hint?: string;
  type?: string;
}

const FIELDS: Record<ChannelKind, FieldSpec[]> = {
  webhook: [
    {
      key: "url",
      label: "URL",
      placeholder: "https://hooks.your-domain/windlass",
    },
    {
      key: "secret",
      label: "Signing secret",
      secret: true,
      hint: "At least 16 characters. Signs each body as X-Windlass-Signature (HMAC-SHA256 of timestamp.body).",
    },
  ],
  ntfy: [
    {
      key: "server",
      label: "Server",
      placeholder: "https://ntfy.sh",
      hint: "Leave empty for ntfy.sh.",
    },
    { key: "topic", label: "Topic", placeholder: "my-windlass-alerts" },
    {
      key: "token",
      label: "Access token",
      secret: true,
      hint: "Only for a protected topic.",
    },
  ],
  discord: [
    {
      key: "url",
      label: "Webhook URL",
      secret: true,
      placeholder: "https://discord.com/api/webhooks/...",
    },
  ],
  slack: [
    {
      key: "url",
      label: "Webhook URL",
      secret: true,
      placeholder: "https://hooks.slack.com/services/...",
    },
  ],
  email: [
    { key: "host", label: "SMTP server", placeholder: "smtp.fastmail.com" },
    { key: "port", label: "Port", placeholder: "587", type: "number" },
    { key: "username", label: "Username" },
    { key: "password", label: "Password", secret: true },
    {
      key: "from",
      label: "From",
      placeholder: "Windlass <alerts@your-domain>",
    },
    {
      key: "to",
      label: "To",
      placeholder: "ops@your-domain, you@your-domain",
      hint: "Separate addresses with commas.",
    },
  ],
};

/**
 * Alert channels: where Windlass sends an alert when something needs a
 * person. Secrets are write-only: the server returns a flag saying one is
 * stored, never the value, and an empty field on edit keeps it.
 */
export function AlertChannels() {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const [editing, setEditing] = useState<AlertChannel | "new" | null>(null);
  const channels = useQuery<AlertChannel[]>({
    queryKey: ["notifications", "channels"],
    queryFn: () => api("/notifications/channels"),
  });
  const deliveries = useQuery<Delivery[]>({
    queryKey: ["notifications", "deliveries"],
    queryFn: () => api("/notifications/deliveries?limit=20"),
    refetchInterval: 30_000,
  });

  const remove = useMutation({
    mutationFn: (id: number) =>
      api(`/notifications/channels/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["notifications"] }),
    onError: (e) => toast.err("Could not remove the channel", errorText(e)),
  });
  const test = useMutation({
    mutationFn: (id: number) =>
      api<{ ok: boolean; error?: string }>(
        `/notifications/channels/${id}/test`,
        { method: "POST" },
      ),
    onSuccess: (res, id) => {
      const name = channels.data?.find((c) => c.id === id)?.name ?? "channel";
      if (res.ok) toast.ok(`Test sent to ${name}`);
      else toast.err(`The test to ${name} failed`, res.error);
    },
    onError: (e) => toast.err("Could not send the test", errorText(e)),
  });

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader
          title="Alert channels"
          description="Where Windlass sends alerts. A failed alert never fails the deploy or backup that raised it; it is retried for about five hours, then shown as failed below."
          actions={
            <Button
              variant="primary"
              icon="plus"
              onClick={() => setEditing("new")}
            >
              Add channel
            </Button>
          }
        />
        {channels.data && channels.data.length > 0 ? (
          <ul className="divide-y divide-hairline border-t border-hairline">
            {channels.data.map((c) => (
              <li
                key={c.id}
                className="flex flex-wrap items-center justify-between gap-3 px-5 py-3"
              >
                <div className="flex min-w-0 flex-wrap items-center gap-2.5 text-sm">
                  <span className="font-medium text-fg">{c.name}</span>
                  <Tag>{KIND_LABEL[c.kind] ?? c.kind}</Tag>
                  {!c.enabled && <StatusPill tone="idle">Off</StatusPill>}
                  <span className="text-fg3">
                    {c.events.length === 0
                      ? "Every problem"
                      : c.events.map((e) => EVENT_LABEL[e] ?? e).join(", ")}
                    {c.project ? ` in ${c.project}` : ""}
                  </span>
                </div>
                <div className="flex flex-none items-center gap-1.5">
                  <Button
                    size="sm"
                    loading={test.isPending && test.variables === c.id}
                    onClick={() => test.mutate(c.id)}
                  >
                    Send test
                  </Button>
                  <IconButton
                    icon="edit"
                    label={`Edit ${c.name}`}
                    onClick={() => setEditing(c)}
                  />
                  <IconButton
                    icon="trash"
                    label={`Remove ${c.name}`}
                    className="hover:bg-err-soft hover:text-err"
                    onClick={async () => {
                      if (
                        await confirm({
                          title: `Remove the ${c.name} channel?`,
                          body: "Alerts stop going to it, and any it has not sent yet are dropped.",
                          confirmLabel: "Remove channel",
                        })
                      )
                        remove.mutate(c.id);
                    }}
                  />
                </div>
              </li>
            ))}
          </ul>
        ) : (
          channels.data && (
            <CardFooter note="No channels yet. Problems show under Needs you on the overview, and nowhere else." />
          )
        )}
      </Card>

      {deliveries.data && deliveries.data.length > 0 && (
        <Card>
          <CardHeader
            title="Recent alerts"
            description="The last 20 deliveries, newest first. Kept for 30 days."
          />
          <ul className="divide-y divide-hairline border-t border-hairline">
            {deliveries.data.map((d) => (
              <li
                key={d.id}
                className="flex flex-wrap items-center gap-2.5 px-5 py-2.5 text-sm"
              >
                <StatusPill
                  tone={
                    d.status === "sent"
                      ? "ok"
                      : d.status === "failed"
                        ? "err"
                        : "warn"
                  }
                >
                  {d.status === "sent"
                    ? "Sent"
                    : d.status === "failed"
                      ? "Failed"
                      : "Retrying"}
                </StatusPill>
                <span className="text-fg">
                  {EVENT_LABEL[d.event] ?? d.event}
                </span>
                {d.project && (
                  <span className="font-mono text-fg2">{d.project}</span>
                )}
                <span className="text-fg3">to {d.channel}</span>
                <span className="ml-auto text-fg3">
                  {timeAgo(d.created_at)}
                </span>
                {d.last_error && d.status !== "sent" && (
                  <span
                    className="w-full truncate text-xs text-err"
                    title={d.last_error}
                  >
                    {d.last_error}
                  </span>
                )}
              </li>
            ))}
          </ul>
        </Card>
      )}

      {editing && (
        <ChannelDialog
          channel={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  );
}

function ChannelDialog({
  channel,
  onClose,
}: {
  channel: AlertChannel | null;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const projects = useProjects();
  const kinds = useQuery<{ events: string[] }>({
    queryKey: ["notifications", "kinds"],
    queryFn: () => api("/notifications/kinds"),
  });
  const [kind, setKind] = useState<ChannelKind>(channel?.kind ?? "ntfy");
  const [name, setName] = useState(channel?.name ?? "");
  const [enabled, setEnabled] = useState(channel?.enabled ?? true);
  const [project, setProject] = useState(channel?.project ?? "");
  const [events, setEvents] = useState<string[]>(channel?.events ?? []);
  const [config, setConfig] = useState<Record<string, string>>(() => {
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(channel?.config ?? {}))
      if (typeof v === "string") out[k] = v;
    if (!channel?.config.security) out.security = "starttls";
    return out;
  });

  const save = useMutation({
    mutationFn: () =>
      api(
        channel
          ? `/notifications/channels/${channel.id}`
          : "/notifications/channels",
        {
          method: channel ? "PUT" : "POST",
          body: JSON.stringify({
            name,
            kind,
            enabled,
            project,
            events,
            config,
          }),
        },
      ),
    onSuccess: () => {
      toast.ok(
        channel ? `Saved ${name}` : `Added ${name}. Send a test to check it.`,
      );
      qc.invalidateQueries({ queryKey: ["notifications"] });
      onClose();
    },
  });

  const toggle = (e: string) =>
    setEvents((cur) =>
      cur.includes(e) ? cur.filter((x) => x !== e) : [...cur, e],
    );

  return (
    <Modal
      onClose={onClose}
      width={560}
      title={channel ? `Edit ${channel.name}` : "Add an alert channel"}
      description="Webhook, Discord and Slack addresses on private, loopback or link-local networks are refused. Email may use a relay on this server."
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            type="submit"
            form="alert-channel"
            loading={save.isPending}
          >
            {channel ? "Save" : "Add channel"}
          </Button>
        </>
      }
    >
      <form
        id="alert-channel"
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Name">
            <Input
              required
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Ops phone"
            />
          </Field>
          <Field label="Kind" hint={channel ? "Fixed once added." : undefined}>
            <Select
              value={kind}
              disabled={!!channel}
              onChange={(e) => setKind(e.target.value as ChannelKind)}
            >
              {(Object.keys(KIND_LABEL) as ChannelKind[]).map((k) => (
                <option key={k} value={k}>
                  {KIND_LABEL[k]}
                </option>
              ))}
            </Select>
          </Field>
          {FIELDS[kind].map((f) => {
            const stored = channel?.config[`${f.key}_set`] === true;
            return (
              <Field
                key={f.key}
                label={f.label}
                hint={
                  stored
                    ? "Stored. Leave empty to keep it, unless you change the server."
                    : f.hint
                }
              >
                <Input
                  type={f.secret ? "password" : (f.type ?? "text")}
                  autoComplete="off"
                  value={config[f.key] ?? ""}
                  placeholder={stored ? "••••••••" : f.placeholder}
                  onChange={(e) =>
                    setConfig((c) => ({ ...c, [f.key]: e.target.value }))
                  }
                />
              </Field>
            );
          })}
          {kind === "email" && (
            <Field label="Security">
              <Select
                value={config.security ?? "starttls"}
                onChange={(e) =>
                  setConfig((c) => ({ ...c, security: e.target.value }))
                }
              >
                <option value="starttls">STARTTLS (port 587)</option>
                <option value="tls">TLS (port 465)</option>
                <option value="none">None (local relay only)</option>
              </Select>
            </Field>
          )}
          <Field label="Project">
            <Select
              value={project}
              onChange={(e) => setProject(e.target.value)}
            >
              <option value="">Every project and the server</option>
              {projects.data?.map((p) => (
                <option key={p.id} value={p.name}>
                  {p.name}
                </option>
              ))}
            </Select>
          </Field>
        </div>

        <Field
          as="div"
          label="Events"
          hint={
            events.length === 0
              ? "None picked means every problem. Deploy succeeded only if picked."
              : undefined
          }
        >
          <div className="flex flex-wrap gap-1.5">
            {(kinds.data?.events ?? []).map((e) => (
              <Button
                key={e}
                size="sm"
                type="button"
                variant={events.includes(e) ? "primary" : "secondary"}
                aria-pressed={events.includes(e)}
                onClick={() => toggle(e)}
              >
                {EVENT_LABEL[e] ?? e}
              </Button>
            ))}
          </div>
        </Field>

        <Switch
          checked={enabled}
          onChange={setEnabled}
          label="On"
          description="A channel that is off keeps its settings. Its alerts wait until it is on again."
        />
        <FormError error={save.error} fallback="Could not save the channel" />
      </form>
    </Modal>
  );
}
