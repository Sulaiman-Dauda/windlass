import { useId, useState } from "react";
import { useCan } from "../api/auth";
import { useServices } from "../api/deployments";
import { useAddDomain, useDomains, useProxyStatus, useRemoveDomain, type Domain } from "../api/domains";
import { Button, IconButton } from "../ui/Button";
import { Card, CardHeader } from "../ui/Card";
import { Input, Field } from "../ui/Field";
import { StatusPill, type Tone } from "../ui/Badge";
import { Callout, FormError } from "../ui/Page";
import { Icon } from "../ui/Icon";
import { Skeleton } from "../ui/Skeleton";
import { useConfirm } from "../ui/Modal";
import { useToast, errorText } from "../ui/Toast";

const statusStyle: Record<Domain["status"], { label: string; tone: Tone }> = {
  active: { label: "Active", tone: "ok" },
  pending: { label: "Waiting for container", tone: "warn" },
  proxy_unavailable: { label: "Proxy unavailable", tone: "err" },
};

export default function DomainsTab({ project }: { project: string }) {
  const domains = useDomains(project);
  const proxy = useProxyStatus();
  const services = useServices(project);
  const add = useAddDomain(project);
  const remove = useRemoveDomain(project);
  const can = useCan();
  const confirm = useConfirm();
  const toast = useToast();
  const listId = useId();

  const [hostname, setHostname] = useState("");
  const [service, setService] = useState("web");
  const [port, setPort] = useState("3000");
  const names = Array.from(new Set(services.data?.services.map((s) => s.service) ?? []));

  const doRemove = async (hostname: string) => {
    const ok = await confirm({
      title: `Remove ${hostname}?`,
      body: "Caddy stops routing this hostname to the project straight away. The DNS record is not touched.",
      confirmLabel: "Remove domain",
    });
    if (!ok) return;
    remove.mutate(hostname, {
      onSuccess: () => toast.ok(`Removed ${hostname}`),
      onError: (e) => toast.err(`Could not remove ${hostname}`, errorText(e)),
    });
  };

  return (
    <div className="max-w-5xl space-y-5">
      {proxy.data && !proxy.data.available && (
        <Callout tone="warn" title="Caddy's admin API is unreachable">
          Domains are saved but won't route until Caddy is running. Everything else keeps working.
        </Callout>
      )}

      {can("member") && (
        <Card>
          <CardHeader
            title="Add a domain"
            description="Point the hostname's DNS A or AAAA record at this server first. Caddy then gets a Let's Encrypt certificate on its own."
          />
          <form
            className="flex flex-wrap items-end gap-3 px-5 py-4"
            onSubmit={(e) => {
              e.preventDefault();
              add.mutate(
                { hostname, service, container_port: parseInt(port, 10) },
                {
                  onSuccess: () => {
                    toast.ok(`Added ${hostname}`, "HTTPS is issued on the first request once DNS resolves here.");
                    setHostname("");
                  },
                },
              );
            }}
          >
            <Field label="Hostname" className="min-w-[220px] flex-[2]">
              <Input
                required
                value={hostname}
                onChange={(e) => setHostname(e.target.value.toLowerCase().trim())}
                placeholder="app.example.com"
                spellCheck={false}
                autoComplete="off"
              />
            </Field>
            <Field label="Service" className="w-40">
              <Input
                required
                list={listId}
                value={service}
                onChange={(e) => setService(e.target.value)}
                spellCheck={false}
                className="font-mono"
              />
              <datalist id={listId}>
                {names.map((n) => (
                  <option key={n} value={n} />
                ))}
              </datalist>
            </Field>
            <Field label="Container port" className="w-32">
              <Input
                required
                type="number"
                min={1}
                max={65535}
                value={port}
                onChange={(e) => setPort(e.target.value)}
                className="font-mono"
              />
            </Field>
            <Button type="submit" variant="primary" icon="plus" loading={add.isPending}>
              Add domain
            </Button>
            <div className="basis-full empty:hidden">
              <FormError error={add.error} fallback="Failed to add the domain" />
            </div>
          </form>
        </Card>
      )}

      <Card className="overflow-hidden">
        <CardHeader title="Domains" description="Routed by Caddy to a service and port inside this project." />
        {domains.isLoading ? (
          <div className="p-5">
            <Skeleton className="h-5 w-1/2" />
          </div>
        ) : domains.data && domains.data.length > 0 ? (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[560px] text-sm">
              <thead>
                <tr className="border-b border-hairline bg-surface2 text-left text-xs text-fg3">
                  <th className="h-9 px-5 font-medium">Hostname</th>
                  <th className="h-9 px-4 font-medium">Routes to</th>
                  <th className="h-9 px-4 font-medium">Status</th>
                  <th className="h-9 w-14" aria-hidden="true" />
                </tr>
              </thead>
              <tbody className="divide-y divide-hairline">
                {domains.data.map((d) => {
                  const st = statusStyle[d.status];
                  return (
                    <tr key={d.hostname} className="transition-colors hover:bg-surface2">
                      <td className="px-5 py-3">
                        <a
                          href={`https://${d.hostname}`}
                          target="_blank"
                          rel="noreferrer"
                          className="inline-flex items-center gap-1.5 font-semibold text-fg hover:text-accent"
                        >
                          <Icon name="lock" size={14} className="text-ok" />
                          {d.hostname}
                          <Icon name="external" size={13} className="text-fg3" />
                        </a>
                      </td>
                      <td className="px-4 py-3 font-mono text-xs text-fg2">
                        {d.service}:{d.container_port}
                      </td>
                      <td className="px-4 py-3">
                        <StatusPill tone={st.tone} live={d.status === "active"}>
                          {st.label}
                        </StatusPill>
                      </td>
                      <td className="px-3 py-3 text-right">
                        {can("member") && (
                          <IconButton
                            icon="trash"
                            label={`Remove ${d.hostname}`}
                            className="hover:bg-err-soft hover:text-err"
                            onClick={() => doRemove(d.hostname)}
                          />
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="px-5 py-8 text-center text-sm text-fg3">No domains yet. The project is reachable only inside Docker's network.</p>
        )}
      </Card>
    </div>
  );
}
