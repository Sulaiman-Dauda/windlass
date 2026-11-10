import { useState } from "react";
import { Link } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { useCan } from "../api/auth";
import { SCOPE_LABEL, useTokens, type CreatedToken } from "../api/tokens";
import { useMCPEndpoint, useMCPTools, type MCPTool } from "../api/mcp";
import { Page, Callout, FormError } from "../ui/Page";
import { Card, CardHeader, CardFooter } from "../ui/Card";
import { Button, btn } from "../ui/Button";
import { Input, Field, Select } from "../ui/Field";
import { Tag } from "../ui/Badge";
import { Icon } from "../ui/Icon";
import { Segmented } from "../ui/Segmented";
import { Skeleton } from "../ui/Skeleton";
import { CopyBlock, CopyField } from "../ui/Copy";
import { timeAgo, formatDate } from "../ui/format";
import { CLIENTS, clientSetup, curlCheck, mcpEndpoint, CHECK_PROMPT, type ClientId } from "../components/agentClients";

// Tokens an agent should get: never one that can edit files or read env values.
type AgentScope = "read" | "deploy";

const TOKEN_DAYS = 90;

// What no tool does, whatever the token. Kept in step with docs/mcp.md.
const NEVER = [
  "Edit Compose files, .env or project files",
  "Read env values",
  "Stop or delete a project",
  "Restore a backup",
  "Change domains, Git settings, users or tokens",
  "Open a terminal",
];

export default function Agents() {
  const can = useCan();
  const [client, setClient] = useState<ClientId>("claude-code");

  // The HTTPS address under the panel domain, else the one the browser reached.
  const domain = useMCPEndpoint();
  const endpoint = domain.data?.url || mcpEndpoint(window.location.origin);
  const plainHTTP = endpoint.startsWith("http:");

  return (
    <Page
      title="AI agents"
      description="Connect Claude Code, Cursor, VS Code and other MCP clients to this server. An agent can see what is running, read logs and deployments, and with a Deploy only token deploy, restart, roll back, promote and back up. It cannot edit Compose files or .env."
    >
      <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_340px]">
        <div className="min-w-0 space-y-6">
          <Card>
            <CardHeader title="1. Endpoint" description="The address your agent connects to. It takes an API token, never your sign-in." />
            <div className="space-y-3 px-5 py-4">
              {domain.isLoading ? <Skeleton className="h-9 rounded-control" /> : <CopyField value={endpoint} />}
              {plainHTTP && (
                <Callout tone="warn" title="This address is plain HTTP">
                  The token travels in a header on every call, so anyone on the network path can read it.{" "}
                  {can("admin") ? (
                    <>
                      Set a{" "}
                      <Link to="/settings/general" className="font-medium text-accent hover:underline">
                        Panel domain
                      </Link>{" "}
                      to serve the panel over HTTPS first.
                    </>
                  ) : (
                    "Ask an admin to serve the panel over HTTPS with a Panel domain."
                  )}
                </Callout>
              )}
            </div>
          </Card>

          <TokenStep client={client} />

          <ConnectStep client={client} setClient={setClient} endpoint={endpoint} />

          <Card>
            <CardHeader title="4. Check it" />
            <div className="space-y-4 px-5 py-4">
              <div>
                <p className="mb-2 text-sm text-fg2">Ask your agent:</p>
                <CopyBlock value={CHECK_PROMPT} label="Copy prompt" />
                <p className="mt-2 text-sm text-fg3">
                  It should call <span className="font-mono">list_projects</span>. Without an agent, list the tools the token
                  can use:
                </p>
              </div>
              <CopyBlock value={curlCheck(endpoint)} label="Copy command" />
            </div>
          </Card>
        </div>

        <aside className="min-w-0 space-y-6">
          <CapabilitiesCard />
          <Card>
            <CardHeader title="Safety" />
            <ul className="space-y-3 px-5 py-4 text-sm text-fg2">
              <li className="flex gap-2.5">
                <Icon name="shield" size={16} className="mt-0.5 flex-none text-fg3" />
                <span>Every write needs the agent to ask you first and send confirm. A model can skip that, so it is a speed bump, not a lock.</span>
              </li>
              <li className="flex gap-2.5">
                <Icon name="checkCircle" size={16} className="mt-0.5 flex-none text-fg3" />
                <span>Keep your client's prompt to approve each tool call on for deploy, restart, rollback, promote and backup.</span>
              </li>
              <li className="flex gap-2.5">
                <Icon name="logs" size={16} className="mt-0.5 flex-none text-fg3" />
                <span>
                  Each write is in the audit log twice: the route's own entry naming the token, and an{" "}
                  <span className="font-mono">mcp.call</span> entry naming the tool.
                </span>
              </li>
            </ul>
            <CardFooter
              note={
                <a href="https://windlass.run/guides/mcp/" target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-accent hover:underline">
                  Full guide <Icon name="external" size={13} />
                </a>
              }
            />
          </Card>
        </aside>
      </div>
    </Page>
  );
}

function TokenStep({ client }: { client: ClientId }) {
  const can = useCan();
  const qc = useQueryClient();
  const tokens = useTokens();
  const [scope, setScope] = useState<AgentScope>("read");
  // null follows the chosen client; typing a name keeps it.
  const [name, setName] = useState<string | null>(null);
  const [created, setCreated] = useState<CreatedToken | null>(null);
  const tokenName = name ?? `${CLIENTS.find((c) => c.id === client)!.tokenName}-agent`;

  const create = useMutation({
    mutationFn: () =>
      api<CreatedToken>("/auth/tokens", {
        method: "POST",
        body: JSON.stringify({ name: tokenName, scope, expires_in_days: TOKEN_DAYS, can_read_secrets: false }),
      }),
    onSuccess: (res) => {
      setCreated(res);
      setName(null);
      qc.invalidateQueries({ queryKey: ["tokens"] });
    },
  });

  const agentTokens = (tokens.data ?? []).filter((t) => !t.revoked_at && !t.expired && (t.scope === "read" || t.scope === "deploy"));

  return (
    <Card>
      <CardHeader
        title="2. Token"
        description="A Read only token can look but not change anything. Give an agent Deploy only when you want it to deploy."
      />
      <div className="space-y-4 px-5 py-4">
        {created && (
          <Callout tone="ok" title={`Copy the ${created.name} token now`} onClose={() => setCreated(null)}>
            <p>It is shown once. Put it in your password manager or secret store, then into WINDLASS_TOKEN in the environment your client starts in. Not in a config file.</p>
            <CopyField value={created.token} className="mt-2.5" />
          </Callout>
        )}
        {can("admin") ? (
          <form
            className="flex flex-wrap items-end gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              create.mutate();
            }}
          >
            <div className="min-w-[180px] flex-1">
              <Field label="Name">
                <Input required maxLength={100} value={tokenName} onChange={(e) => setName(e.target.value)} spellCheck={false} />
              </Field>
            </div>
            <Field label="Access" as="div">
              <Segmented
                label="Access"
                value={scope}
                onChange={setScope}
                options={[
                  { value: "read", label: SCOPE_LABEL.read },
                  { value: "deploy", label: SCOPE_LABEL.deploy },
                ]}
              />
            </Field>
            <Button type="submit" variant="primary" icon="plus" loading={create.isPending}>
              Create token
            </Button>
            <p className="basis-full text-xs text-fg3">Expires after {TOKEN_DAYS} days. Other access levels and expiries are under Settings, API tokens.</p>
            <div className="basis-full empty:hidden">
              <FormError error={create.error} fallback="Could not create the token" />
            </div>
          </form>
        ) : (
          <p className="text-sm text-fg3">Only admins can create tokens. Ask an admin for a Read only or Deploy only token.</p>
        )}
      </div>
      {agentTokens.length > 0 && (
        <ul className="divide-y divide-hairline border-t border-hairline">
          {agentTokens.map((t) => (
            <li key={t.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-5 py-2.5 text-sm">
              <Icon name="key" size={16} className="text-fg2" />
              <span className="font-medium text-fg">{t.name}</span>
              <span className="font-mono text-xs text-fg3">{t.prefix}…</span>
              <Tag>{SCOPE_LABEL[t.scope]}</Tag>
              <span className="text-xs text-fg3">Expires {formatDate(t.expires_at)}</span>
              <span className="text-xs text-fg3">{t.last_used_at ? `Used ${timeAgo(t.last_used_at)}` : "Never used"}</span>
            </li>
          ))}
        </ul>
      )}
      <CardFooter
        note={
          <>
            Revoke a token under{" "}
            <Link to="/settings/tokens" className="font-medium text-accent hover:underline">
              Settings, API tokens
            </Link>
            .
          </>
        }
      />
    </Card>
  );
}

function ConnectStep({ client, setClient, endpoint }: { client: ClientId; setClient: (c: ClientId) => void; endpoint: string }) {
  const setup = clientSetup(client, endpoint);
  return (
    <Card>
      <CardHeader title="3. Connect your client" />
      <div className="space-y-4 px-5 py-4">
        <div className="hidden sm:block">
          <Segmented label="Client" value={client} onChange={setClient} options={CLIENTS.map((c) => ({ value: c.id, label: c.label }))} />
        </div>
        <Field label="Client" className="sm:hidden">
          <Select value={client} onChange={(e) => setClient(e.target.value as ClientId)}>
            {CLIENTS.map((c) => (
              <option key={c.id} value={c.id}>
                {c.label}
              </option>
            ))}
          </Select>
        </Field>
        {setup.install && (
          <div className="flex flex-wrap items-center gap-3">
            <a href={setup.install.href} className={btn("primary", "md")}>
              <Icon name="download" size={16} /> {setup.install.label}
            </a>
            <span className="text-sm text-fg3">{setup.install.note}</span>
          </div>
        )}
        {setup.snippets.map((s) => (
          <div key={s.title}>
            <p className="mb-2 text-sm text-fg2">
              {s.title}
              {s.path && (
                <>
                  {" "}
                  <span className="font-mono text-fg">{s.path}</span>
                </>
              )}
            </p>
            <CopyBlock value={s.code} />
          </div>
        ))}
        {setup.note && <p className="text-sm text-fg3">{setup.note}</p>}
      </div>
    </Card>
  );
}

function CapabilitiesCard() {
  const tools = useMCPTools();
  const read = (tools.data ?? []).filter((t) => t.needs === "read");
  const deploy = (tools.data ?? []).filter((t) => t.needs === "deploy");
  return (
    <Card>
      <CardHeader title="What it can do" />
      <div className="space-y-4 px-5 py-4">
        {tools.isLoading && <Skeleton className="h-40 rounded-control" />}
        {tools.isError && <FormError error={tools.error} fallback="Could not load the tool list" />}
        {tools.data && (
          <>
            <ToolGroup title="Read, any token" tools={read} />
            <ToolGroup title="Deploy, with a token that can deploy" tools={deploy} />
          </>
        )}
        <div>
          <h4 className="mb-1.5 text-xs font-semibold text-fg3">Never</h4>
          <ul className="space-y-1 text-sm text-fg2">
            {NEVER.map((n) => (
              <li key={n} className="flex gap-2">
                <Icon name="x" size={15} className="mt-0.5 flex-none text-fg3" />
                {n}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </Card>
  );
}

function ToolGroup({ title, tools }: { title: string; tools: MCPTool[] }) {
  return (
    <div>
      <h4 className="mb-1.5 text-xs font-semibold text-fg3">{title}</h4>
      <ul className="space-y-1 text-sm">
        {tools.map((t) => (
          <li key={t.name} className="flex items-baseline justify-between gap-3">
            <span className="text-fg2">{t.summary}</span>
            <span className="font-mono text-xs text-fg3">{t.name}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
