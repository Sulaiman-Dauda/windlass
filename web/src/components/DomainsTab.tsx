import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { Button } from "../ui/Button";
import { Input, Field } from "../ui/Field";
import { StatusPill, type Tone } from "../ui/Badge";
import { Icon } from "../ui/Icon";

interface Domain {
  hostname: string;
  service: string;
  container_port: number;
  status: "active" | "pending" | "proxy_unavailable";
}

interface ProxyInfo {
  available: boolean;
  version: string;
}

const statusStyle: Record<Domain["status"], { label: string; tone: Tone }> = {
  active: { label: "active", tone: "ok" },
  pending: { label: "waiting for container", tone: "warn" },
  proxy_unavailable: { label: "proxy unavailable", tone: "err" },
};

export default function DomainsTab({ project }: { project: string }) {
  const qc = useQueryClient();
  const domains = useQuery<Domain[]>({
    queryKey: ["projects", project, "domains"],
    queryFn: () => api(`/projects/${project}/domains`),
    refetchInterval: 5000,
  });
  const proxyStatus = useQuery<ProxyInfo>({
    queryKey: ["proxy", "status"],
    queryFn: () => api("/proxy/status"),
  });

  const add = useMutation({
    mutationFn: (body: { hostname: string; service: string; container_port: number }) =>
      api(`/projects/${project}/domains`, { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["projects", project, "domains"] }),
  });
  const remove = useMutation({
    mutationFn: (hostname: string) =>
      api(`/projects/${project}/domains/${hostname}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["projects", project, "domains"] }),
  });

  const [hostname, setHostname] = useState("");
  const [service, setService] = useState("web");
  const [port, setPort] = useState("3000");

  return (
    <div className="max-w-3xl">
      {proxyStatus.data && !proxyStatus.data.available && (
        <div className="mb-4 flex items-start gap-2.5 rounded-[10px] bg-warn-soft px-3.5 py-3 text-sm text-warn">
          <Icon name="warning" size={16} className="mt-0.5 flex-none" />
          <span>
            Caddy's admin API is unreachable. Domains are saved but won't route
            until Caddy is running (see docs/install). Everything else keeps
            working.
          </span>
        </div>
      )}

      <p className="text-sm leading-relaxed text-fg2">
        Point your DNS at this server, add the hostname here, and Caddy
        provisions HTTPS automatically via Let's Encrypt.
      </p>

      <form
        className="mt-4 flex items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          add.mutate(
            { hostname, service, container_port: parseInt(port, 10) },
            { onSuccess: () => setHostname("") },
          );
        }}
      >
        <Field label="Hostname" className="flex-1">
          <Input
            required
            value={hostname}
            onChange={(e) => setHostname(e.target.value.toLowerCase())}
            placeholder="app.example.com"
          />
        </Field>
        <Field label="Service" className="w-28">
          <Input
            required
            value={service}
            onChange={(e) => setService(e.target.value)}
          />
        </Field>
        <Field label="Port" className="w-24">
          <Input
            required
            type="number"
            min={1}
            max={65535}
            value={port}
            onChange={(e) => setPort(e.target.value)}
          />
        </Field>
        <Button type="submit" variant="primary" disabled={add.isPending}>
          <Icon name="plus" size={15} />
          Add domain
        </Button>
      </form>
      {add.isError && (
        <p className="mt-2 text-sm text-err">
          {add.error instanceof Error ? add.error.message : "Failed to add domain"}
        </p>
      )}

      <div className="mt-6 space-y-2">
        {domains.data?.map((d) => {
          const st = statusStyle[d.status];
          return (
            <div
              key={d.hostname}
              className="flex items-center justify-between rounded-[10px] border border-hairline bg-surface2 px-4 py-3"
            >
              <div>
                <a
                  href={`https://${d.hostname}`}
                  target="_blank"
                  rel="noreferrer"
                  className="text-sm font-medium text-fg hover:underline"
                >
                  {d.hostname}
                </a>
                <div className="mt-0.5 text-xs text-fg3">
                  → {d.service}:{d.container_port}
                </div>
              </div>
              <div className="flex items-center gap-4">
                <StatusPill tone={st.tone}>{st.label}</StatusPill>
                <button
                  onClick={() => remove.mutate(d.hostname)}
                  className="text-xs text-fg3 hover:text-err"
                >
                  Remove
                </button>
              </div>
            </div>
          );
        })}
        {domains.data?.length === 0 && (
          <p className="text-sm text-fg3">No domains yet.</p>
        )}
      </div>
    </div>
  );
}
