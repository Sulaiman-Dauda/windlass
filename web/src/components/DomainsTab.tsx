import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";

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

const statusStyle: Record<Domain["status"], { label: string; cls: string }> = {
  active: { label: "active", cls: "text-emerald-400" },
  pending: { label: "waiting for container", cls: "text-amber-400" },
  proxy_unavailable: { label: "proxy unavailable", cls: "text-red-400" },
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
        <div className="mb-4 rounded-md border border-amber-900/60 bg-amber-950/30 px-4 py-3 text-sm text-amber-300">
          Caddy's admin API is unreachable. Domains are saved but won't route
          until Caddy is running (see docs/install). Everything else keeps
          working.
        </div>
      )}

      <p className="text-sm text-zinc-500">
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
        <label className="flex-1">
          <span className="mb-1 block text-xs text-zinc-500">Hostname</span>
          <input
            required
            value={hostname}
            onChange={(e) => setHostname(e.target.value.toLowerCase())}
            placeholder="app.example.com"
            className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          />
        </label>
        <label>
          <span className="mb-1 block text-xs text-zinc-500">Service</span>
          <input
            required
            value={service}
            onChange={(e) => setService(e.target.value)}
            className="w-28 rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          />
        </label>
        <label>
          <span className="mb-1 block text-xs text-zinc-500">Port</span>
          <input
            required
            type="number"
            min={1}
            max={65535}
            value={port}
            onChange={(e) => setPort(e.target.value)}
            className="w-24 rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          />
        </label>
        <button
          type="submit"
          disabled={add.isPending}
          className="rounded-md bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
        >
          Add domain
        </button>
      </form>
      {add.isError && (
        <p className="mt-2 text-sm text-red-400">
          {add.error instanceof Error ? add.error.message : "Failed to add domain"}
        </p>
      )}

      <div className="mt-6 space-y-2">
        {domains.data?.map((d) => {
          const st = statusStyle[d.status];
          return (
            <div
              key={d.hostname}
              className="flex items-center justify-between rounded-md border border-zinc-900 bg-zinc-900/40 px-4 py-3"
            >
              <div>
                <a
                  href={`https://${d.hostname}`}
                  target="_blank"
                  rel="noreferrer"
                  className="text-sm font-medium hover:underline"
                >
                  {d.hostname}
                </a>
                <div className="mt-0.5 text-xs text-zinc-500">
                  → {d.service}:{d.container_port}
                </div>
              </div>
              <div className="flex items-center gap-4">
                <span className={`text-xs ${st.cls}`}>{st.label}</span>
                <button
                  onClick={() => remove.mutate(d.hostname)}
                  className="text-xs text-zinc-500 hover:text-red-400"
                >
                  Remove
                </button>
              </div>
            </div>
          );
        })}
        {domains.data?.length === 0 && (
          <p className="text-sm text-zinc-600">No domains yet.</p>
        )}
      </div>
    </div>
  );
}
