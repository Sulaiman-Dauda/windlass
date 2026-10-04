import { useEffect, useState, type ReactNode } from "react";
import { Link, NavLink, Navigate, useLocation, useParams, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { useMe, useCan } from "../api/auth";
import { useUpdateCheck } from "../api/system";
import { Page, Callout, FormError } from "../ui/Page";
import { Card, CardHeader, CardFooter } from "../ui/Card";
import { Button, IconButton, btn } from "../ui/Button";
import { Input, Select, Field } from "../ui/Field";
import { StatusPill, Tag } from "../ui/Badge";
import { Icon, type IconName } from "../ui/Icon";
import { Segmented } from "../ui/Segmented";
import { CopyField } from "../ui/Copy";
import { useTheme, type ThemeMode } from "../ui/theme";
import { useConfirm } from "../ui/Modal";
import { useToast, errorText } from "../ui/Toast";
import { cn } from "../ui/cn";
import { formatBytes } from "../ui/format";

interface Connection {
  id: number;
  provider: string;
  name: string;
}

// Each tab is a real route (/settings/<tab>) so sections are addressable:
// the sidebar update alert links to system, and OAuth redirects land on git.
// Admin tabs mirror the server, where /git, /registries and /system/* are admin-only.
const TABS: { value: string; label: string; icon: IconName; admin?: boolean }[] = [
  { value: "general", label: "General", icon: "settings" },
  { value: "auth", label: "Users and sign-in", icon: "users" },
  { value: "git", label: "Git", icon: "gitBranch", admin: true },
  { value: "registries", label: "Registries", icon: "package", admin: true },
  { value: "system", label: "System", icon: "server", admin: true },
];

export default function Settings() {
  const { tab } = useParams();
  const can = useCan();
  const tabs = TABS.filter((t) => !t.admin || can("admin"));
  const current = tabs.find((t) => t.value === tab);

  if (!current) {
    return <Navigate to="/settings/general" replace />;
  }

  return (
    <Page
      title="Settings"
      crumbs={[{ label: "Settings", to: "/settings/general" }, { label: current.label }]}
      description="Server-wide configuration. Changes here apply to every project."
    >
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[208px_minmax(0,1fr)] lg:gap-10">
        <nav
          aria-label="Settings sections"
          className="-mx-1 flex gap-1 overflow-x-auto px-1 [scrollbar-width:none] lg:sticky lg:top-16 lg:h-fit lg:flex-col lg:overflow-visible"
        >
          {tabs.map((t) => (
            <NavLink
              key={t.value}
              to={`/settings/${t.value}`}
              className={({ isActive }) =>
                cn(
                  "flex h-8 flex-none items-center gap-2.5 rounded-control px-2.5 text-sm transition-colors",
                  isActive ? "bg-sunken font-semibold text-fg" : "font-medium text-fg2 hover:bg-sunken hover:text-fg",
                )
              }
            >
              {({ isActive }) => (
                <>
                  <Icon name={t.icon} size={16} className={isActive ? "text-accent" : "text-fg3"} />
                  {t.label}
                </>
              )}
            </NavLink>
          ))}
        </nav>

        <div className="min-w-0 space-y-6">
          {tab === "general" && (
            <>
              <AppearanceSection />
              {can("admin") && <PanelDomainSection />}
            </>
          )}
          {tab === "auth" && (
            <>
              <UsersSection />
              <SecuritySection />
              <GitHubAppSection />
              <OAuthAppsSection />
            </>
          )}
          {tab === "git" && <GitConnections />}
          {tab === "registries" && <RegistryCredentials />}
          {tab === "system" && (
            <>
              <UpdateSection />
              <DockerStorageSection />
            </>
          )}
        </div>
      </div>
    </Page>
  );
}

// ---------- Layout helpers ----------

/** A labelled row inside a settings card: text on the left, control on the right. */
function Row({ title, desc, children }: { title: ReactNode; desc?: ReactNode; children?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 px-5 py-4">
      <div className="min-w-0 flex-1">
        <div className="text-sm font-semibold text-fg">{title}</div>
        {desc && <div className="mt-0.5 text-sm text-fg3">{desc}</div>}
      </div>
      {children && <div className="flex flex-none flex-wrap items-center gap-2.5">{children}</div>}
    </div>
  );
}

function ListRow({ children, onRemove, removeLabel }: { children: ReactNode; onRemove?: () => void; removeLabel: string }) {
  return (
    <li className="flex items-center justify-between gap-3 px-5 py-3">
      <div className="flex min-w-0 flex-wrap items-center gap-2.5 text-sm">{children}</div>
      {onRemove && (
        <IconButton icon="trash" label={removeLabel} className="hover:bg-err-soft hover:text-err" onClick={onRemove} />
      )}
    </li>
  );
}

// ---------- Appearance ----------

function AppearanceSection() {
  const { mode, setMode } = useTheme();
  return (
    <Card>
      <CardHeader title="Appearance" description="Applies to this browser only." />
      <Row title="Theme" desc="Match system follows your device between light and dark.">
        <Segmented<ThemeMode>
          label="Theme"
          value={mode}
          onChange={setMode}
          options={[
            { value: "light", label: "Light", icon: <Icon name="sun" size={15} /> },
            { value: "system", label: "Match system", icon: <Icon name="monitor" size={15} /> },
            { value: "dark", label: "Dark", icon: <Icon name="moon" size={15} /> },
          ]}
        />
      </Row>
    </Card>
  );
}

// ---------- Panel domain ----------

interface PanelDomainStatus {
  hostname: string;
  url?: string;
  configured: boolean;
  proxy_available: boolean;
}

function PanelDomainSection() {
  const qc = useQueryClient();
  const toast = useToast();
  const status = useQuery<PanelDomainStatus>({
    queryKey: ["system", "panel-domain"],
    queryFn: () => api("/system/panel-domain"),
    retry: false,
  });
  const [hostname, setHostname] = useState("");
  useEffect(() => {
    if (status.data) setHostname(status.data.hostname);
  }, [status.data]);
  const save = useMutation<PanelDomainStatus, Error, string>({
    mutationFn: (value) => api("/system/panel-domain", { method: "PUT", body: JSON.stringify({ hostname: value }) }),
    onSuccess: (_d, value) => toast.ok(value ? "Panel domain saved" : "Panel domain removed"),
    onSettled: () => qc.invalidateQueries({ queryKey: ["system", "panel-domain"] }),
  });

  if (status.isError) return null;
  return (
    <Card>
      <CardHeader
        title="Panel domain"
        description="Serve this panel on its own hostname over HTTPS. Point the name's DNS A or AAAA record at the server first; Windlass adds its own Caddy route."
      />
      <form
        id="panel-domain"
        className="px-5 py-4"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate(hostname.trim());
        }}
      >
        <Field label="Hostname" className="max-w-md">
          <Input
            value={hostname}
            onChange={(e) => setHostname(e.target.value.toLowerCase())}
            placeholder="windlass.example.com"
            spellCheck={false}
          />
        </Field>
        {status.data?.configured && (
          <div className="mt-3 flex flex-wrap items-center gap-3">
            <StatusPill tone={status.data.proxy_available ? "ok" : "warn"} live={status.data.proxy_available}>
              {status.data.proxy_available ? "Active" : "Caddy unavailable"}
            </StatusPill>
            <a className="font-mono text-sm text-accent hover:underline" href={status.data.url}>
              {status.data.url}
            </a>
          </div>
        )}
        <div className="mt-3 empty:hidden">
          <FormError error={save.error} fallback="Could not configure the panel domain" />
        </div>
      </form>
      <CardFooter note="The panel stays reachable on its port as well.">
        {status.data?.configured && (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setHostname("");
              save.mutate("");
            }}
          >
            Remove
          </Button>
        )}
        <Button type="submit" form="panel-domain" size="sm" variant="primary" loading={save.isPending}>
          Save
        </Button>
      </CardFooter>
    </Card>
  );
}

// ---------- Two-factor ----------

function SecuritySection() {
  const [enroll, setEnroll] = useState<{ secret: string; otpauth_url: string } | null>(null);
  const [code, setCode] = useState("");
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const me = useMe();

  const begin = useMutation({
    mutationFn: () => api<{ secret: string; otpauth_url: string }>("/auth/totp/setup", { method: "POST" }),
    onSuccess: setEnroll,
  });
  const verify = useMutation({
    mutationFn: () => api("/auth/totp/verify", { method: "POST", body: JSON.stringify({ code }) }),
    onSuccess: () => {
      setEnroll(null);
      setCode("");
      toast.ok("Two-factor authentication is on");
      qc.invalidateQueries({ queryKey: ["auth", "me"] });
    },
  });
  const disable = useMutation({
    mutationFn: () => api("/auth/totp/disable", { method: "POST" }),
    onSuccess: () => {
      toast.ok("Two-factor authentication is off");
      qc.invalidateQueries({ queryKey: ["auth", "me"] });
    },
  });

  return (
    <Card>
      <CardHeader title="Two-factor authentication" description="For your account. Protects sign-in with a code from an authenticator app." />
      {me.data?.totp_enabled ? (
        <Row title="Authenticator app" desc="A code is required every time you sign in.">
          <StatusPill tone="ok">Enabled</StatusPill>
          <Button
            size="sm"
            onClick={async () => {
              if (
                await confirm({
                  title: "Turn off two-factor authentication?",
                  body: "Signing in will need only your password.",
                  confirmLabel: "Turn off",
                })
              )
                disable.mutate();
            }}
          >
            Disable
          </Button>
        </Row>
      ) : enroll ? (
        <div className="space-y-3 px-5 py-4">
          <p className="text-sm text-fg2">
            Add this secret to your authenticator app (or open the link on your phone), then enter the 6-digit code it shows.
          </p>
          <Field as="div" label="Secret">
            <CopyField value={enroll.secret} />
          </Field>
          <Field as="div" label="Setup link">
            <CopyField value={enroll.otpauth_url} />
          </Field>
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              verify.mutate();
            }}
          >
            <Field label="Code" className="w-40">
              <Input
                autoFocus
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
                placeholder="123456"
                className="text-center font-mono tracking-[0.3em]"
              />
            </Field>
            <Button type="submit" variant="primary" disabled={code.length !== 6} loading={verify.isPending}>
              Confirm
            </Button>
            <Button variant="ghost" onClick={() => setEnroll(null)}>
              Cancel
            </Button>
          </form>
          <FormError error={verify.error} fallback="Invalid code" />
        </div>
      ) : (
        <Row title="Authenticator app" desc="Any TOTP app works: 1Password, Authy, Google Authenticator.">
          <Button size="sm" icon="shield" onClick={() => begin.mutate()} loading={begin.isPending}>
            Set up
          </Button>
        </Row>
      )}
    </Card>
  );
}

// ---------- GitHub App ----------

interface GitHubAppStatus {
  configured: boolean;
  slug?: string;
  owner?: string;
  html_url?: string;
}

const githubAppErrors: Record<string, string> = {
  state_mismatch: "The creation state did not match. Try again.",
  missing_code: "GitHub did not return a creation code. Try again.",
  conversion_failed: "GitHub rejected the app manifest exchange. Try again.",
};

function GitHubAppSection() {
  const can = useCan();
  const app = useQuery<GitHubAppStatus>({
    queryKey: ["system", "github-app"],
    queryFn: () => api("/system/github-app"),
    retry: false,
    enabled: can("admin"),
  });

  const [searchParams, setSearchParams] = useSearchParams();
  const created = searchParams.get("github_app");
  const appError = searchParams.get("github_app_error");

  if (!can("admin") || app.isError) return null;

  return (
    <Card>
      <CardHeader
        title="GitHub App"
        description="Connect GitHub in two clicks. Windlass sends GitHub a pre-filled app manifest; you confirm once and the credentials come back on their own: repository access and push deploys, nothing to copy."
      />
      <div className="space-y-3 px-5 py-4">
        {created && (
          <Callout tone="ok" title={`GitHub App ${created} created`} onClose={() => setSearchParams({}, { replace: true })}>
            Install it on your repositories from{" "}
            <Link to="/settings/git" className="underline">
              Settings, Git
            </Link>
            . To also sign in with GitHub, add the “Email addresses: read” account permission on GitHub: manifests
            cannot request it.
          </Callout>
        )}
        {appError && (
          <Callout tone="err" onClose={() => setSearchParams({}, { replace: true })}>
            {githubAppErrors[appError] ?? "GitHub App creation failed."}
          </Callout>
        )}

        {app.data?.configured ? (
          <div className="flex flex-wrap items-center gap-3">
            <StatusPill tone="ok">Configured</StatusPill>
            <span className="text-sm">
              <span className="font-mono font-medium text-fg">{app.data.slug}</span>
              {app.data.owner && <span className="text-fg3"> · owned by {app.data.owner}</span>}
            </span>
            <span className="ml-auto flex gap-2">
              {app.data.html_url && (
                <a href={app.data.html_url} target="_blank" rel="noreferrer" className={btn("secondary", "sm")}>
                  Manage on GitHub <Icon name="external" size={14} />
                </a>
              )}
              <Link to="/settings/git" className={btn("secondary", "sm")}>
                Install on repositories
              </Link>
            </span>
          </div>
        ) : (
          <a href="/api/v1/system/github-app/create" className={btn("primary", "md")}>
            <Icon name="github" size={16} /> Create GitHub App
          </a>
        )}
      </div>
    </Card>
  );
}

// ---------- OAuth applications ----------

function OAuthAppsSection() {
  const qc = useQueryClient();
  const can = useCan();
  const toast = useToast();
  const providers = useQuery<{ github: boolean; google: boolean }>({
    queryKey: ["auth", "oauth-providers"],
    queryFn: () => api("/auth/oauth/providers"),
  });

  const [provider, setProvider] = useState("github");
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");

  const save = useMutation({
    mutationFn: () =>
      api(`/system/oauth/${provider}`, {
        method: "PUT",
        body: JSON.stringify({ client_id: clientId, client_secret: clientSecret }),
      }),
    onSuccess: () => {
      setClientId("");
      setClientSecret("");
      toast.ok(`${provider === "github" ? "GitHub" : "Google"} sign-in configured`);
      qc.invalidateQueries({ queryKey: ["auth", "oauth-providers"] });
    },
  });

  if (!can("admin")) return null;
  const callbackUrl = `${window.location.origin}/api/v1/auth/oauth/${provider}/callback`;

  return (
    <Card>
      <CardHeader
        title="Sign in with GitHub or Google"
        description="Manual setup: register an OAuth app with the callback URL below, then paste its credentials. Needed for Google; for GitHub, the GitHub App above is quicker."
        actions={
          <span className="flex gap-1.5">
            <StatusPill tone={providers.data?.github ? "ok" : "idle"}>GitHub</StatusPill>
            <StatusPill tone={providers.data?.google ? "ok" : "idle"}>Google</StatusPill>
          </span>
        }
      />
      <form
        id="oauth-app"
        className="space-y-4 px-5 py-4"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Provider" className="w-36">
            <Select value={provider} onChange={(e) => setProvider(e.target.value)}>
              <option value="github">GitHub</option>
              <option value="google">Google</option>
            </Select>
          </Field>
          <Field as="div" label="Callback URL" className="min-w-[240px] flex-1">
            <CopyField value={callbackUrl} />
          </Field>
        </div>
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Client ID" className="min-w-[200px] flex-1">
            <Input required value={clientId} onChange={(e) => setClientId(e.target.value)} className="font-mono" />
          </Field>
          <Field label="Client secret" className="min-w-[200px] flex-1">
            <Input required type="password" autoComplete="off" value={clientSecret} onChange={(e) => setClientSecret(e.target.value)} />
          </Field>
        </div>
        <FormError error={save.error} fallback="Failed to save" />
      </form>
      <CardFooter note="The secret is stored encrypted and never shown again.">
        <Button type="submit" form="oauth-app" size="sm" variant="primary" loading={save.isPending}>
          Save credentials
        </Button>
      </CardFooter>
    </Card>
  );
}

// ---------- Git connections ----------

const gitErrorMessages: Record<string, string> = {
  not_configured: "The GitHub OAuth app is not configured.",
  state_mismatch: "The authorisation state did not match. Try connecting again.",
  exchange_failed: "GitHub rejected the authorisation code. Try connecting again.",
  profile_failed: "Connected, but the GitHub profile could not be read.",
  app_install_failed: "The GitHub App installation could not be linked. Try again.",
};

function GitConnections() {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const connections = useQuery<Connection[]>({ queryKey: ["git", "connections"], queryFn: () => api("/git/connections") });
  const providers = useQuery<{ github: boolean; google: boolean }>({
    queryKey: ["auth", "oauth-providers"],
    queryFn: () => api("/auth/oauth/providers"),
  });
  const app = useQuery<GitHubAppStatus>({
    queryKey: ["system", "github-app"],
    queryFn: () => api("/system/github-app"),
    retry: false,
  });

  const [searchParams, setSearchParams] = useSearchParams();
  const connected = searchParams.get("git_connected");
  const gitError = searchParams.get("git_error");

  const [provider, setProvider] = useState("github");
  const [name, setName] = useState("");
  const [token, setToken] = useState("");
  const [manualOpen, setManualOpen] = useState(false);

  const add = useMutation({
    mutationFn: () => api("/git/connections", { method: "POST", body: JSON.stringify({ provider, name, token }) }),
    onSuccess: () => {
      setName("");
      setToken("");
      setManualOpen(false);
      toast.ok("Connection added");
      qc.invalidateQueries({ queryKey: ["git", "connections"] });
    },
  });
  const remove = useMutation({
    mutationFn: (id: number) => api(`/git/connections/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["git", "connections"] }),
    onError: (e) => toast.err("Could not remove the connection", errorText(e)),
  });

  return (
    <Card>
      <CardHeader
        title="Git connections"
        description="Access to private repositories. Tokens are stored encrypted and never written to disk."
        actions={
          <Button size="sm" variant="ghost" onClick={() => setManualOpen((o) => !o)}>
            {manualOpen ? "Cancel" : "Add a token manually"}
          </Button>
        }
      />
      <div className="space-y-4 px-5 py-4">
        {connected && (
          <Callout tone="ok" onClose={() => setSearchParams({}, { replace: true })}>
            GitHub account connected as <span className="font-mono">{connected}</span>.
          </Callout>
        )}
        {gitError && (
          <Callout tone="err" onClose={() => setSearchParams({}, { replace: true })}>
            {gitErrorMessages[gitError] ?? "GitHub connect failed."}
          </Callout>
        )}

        {app.data?.configured ? (
          <a href={`https://github.com/apps/${app.data.slug}/installations/new`} className={btn("primary", "md")}>
            <Icon name="github" size={16} /> Install GitHub App on repositories
          </a>
        ) : providers.data?.github ? (
          <a href="/api/v1/git/connections/github/connect" className={btn("primary", "md")}>
            <Icon name="github" size={16} /> Connect GitHub
          </a>
        ) : (
          <p className="text-sm text-fg3">
            Create the GitHub App in{" "}
            <Link to="/settings/auth" className="text-accent hover:underline">
              Users and sign-in
            </Link>{" "}
            for two-click connect, or add a token manually.
          </p>
        )}

        {manualOpen && (
          <form
            className="flex flex-wrap items-end gap-3 rounded-card border border-hairline bg-surface2 p-4"
            onSubmit={(e) => {
              e.preventDefault();
              add.mutate();
            }}
          >
            <Field label="Provider" className="w-32">
              <Select value={provider} onChange={(e) => setProvider(e.target.value)}>
                <option value="github">GitHub</option>
                <option value="gitlab">GitLab</option>
              </Select>
            </Field>
            <Field label="Name" className="w-44">
              <Input required value={name} onChange={(e) => setName(e.target.value)} placeholder="acme-bot" />
            </Field>
            <Field label="Token" className="min-w-[200px] flex-1">
              <Input required type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} placeholder="ghp_… or glpat-…" />
            </Field>
            <Button type="submit" variant="primary" loading={add.isPending}>
              Add
            </Button>
            <div className="basis-full empty:hidden">
              <FormError error={add.error} fallback="Failed" />
            </div>
          </form>
        )}
      </div>

      {connections.data && connections.data.length > 0 && (
        <ul className="divide-y divide-hairline border-t border-hairline">
          {connections.data.map((c) => (
            <ListRow
              key={c.id}
              removeLabel={`Remove ${c.name}`}
              onRemove={async () => {
                if (
                  await confirm({
                    title: `Remove ${c.name}?`,
                    body: "Projects that pull private repositories through it will fail to deploy until they get another connection.",
                    confirmLabel: "Remove connection",
                  })
                )
                  remove.mutate(c.id);
              }}
            >
              <Icon name={c.provider === "github" ? "github" : "gitBranch"} size={16} className="text-fg2" />
              <span className="font-mono font-medium text-fg">{c.name}</span>
              <Tag>{c.provider}</Tag>
            </ListRow>
          ))}
        </ul>
      )}
    </Card>
  );
}

// ---------- Registries ----------

interface RegistryCredential {
  id: number;
  host: string;
  username: string;
  updated_at: string;
  verified_at?: string;
}

/**
 * Container registry credentials.
 *
 * Applied to the host with a real `docker login`, not held inside Windlass, so
 * `docker compose pull` keeps working with the panel stopped. That is the
 * promise in docs/life-without-the-panel, and it is worth saying on the screen
 * so nobody assumes the panel is doing something clever and unremovable.
 */
function RegistryCredentials() {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const creds = useQuery<RegistryCredential[]>({
    queryKey: ["registries"],
    queryFn: () => api("/registries"),
  });
  // GitHub's registry takes a GitHub token, so an account that is already
  // connected saves finding a second one. Offered rather than applied on
  // connect: copying a repo-scoped token into the registry store without
  // asking would leave a wider secret about than the job needs.
  const gitConns = useQuery<Connection[]>({
    queryKey: ["git", "connections"],
    queryFn: () => api("/git/connections"),
  });
  const github = gitConns.data?.find((c) => c.provider === "github");

  const [host, setHost] = useState("ghcr.io");
  const [username, setUsername] = useState("");
  const [secret, setSecret] = useState("");
  const [warning, setWarning] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () =>
      api<{ credential: RegistryCredential; warning?: string }>("/registries", {
        method: "PUT",
        body: JSON.stringify({ host, username, secret }),
      }),
    onSuccess: (res) => {
      setSecret("");
      // Stored but the login failed: worth saying, because the credential is
      // saved and somebody would otherwise assume it works.
      setWarning(res.warning ?? null);
      if (!res.warning) toast.ok(`Signed in to ${host}`);
      qc.invalidateQueries({ queryKey: ["registries"] });
    },
  });
  const remove = useMutation({
    mutationFn: (id: number) => api(`/registries/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["registries"] }),
    onError: (e) => toast.err("Could not remove the credential", errorText(e)),
  });
  const fromGit = useMutation({
    mutationFn: (id: number) =>
      api<{ credential: RegistryCredential | null }>(`/registries/from-git/${id}`, { method: "POST" }),
    onSuccess: (res) => {
      setWarning(
        res.credential && !res.credential.verified_at
          ? "Stored, but that connection's token cannot pull packages. Add read:packages to it, or enter a registry token below."
          : null,
      );
      qc.invalidateQueries({ queryKey: ["registries"] });
    },
  });

  return (
    <Card>
      <CardHeader
        title="Container registries"
        description="Credentials for private images. Applied to the host with docker login, so pulls keep working if Windlass is stopped or removed. Tokens are stored encrypted and never returned."
      />
      <div className="space-y-4 px-5 py-4">
        {warning && (
          <Callout tone="err" title="Saved, but signing in failed" onClose={() => setWarning(null)}>
            {warning}
          </Callout>
        )}

        {github && (
          <div className="flex flex-wrap items-center gap-3 rounded-card border border-hairline bg-surface2 px-4 py-3">
            <Icon name="github" size={18} className="text-fg2" />
            <span className="min-w-0 flex-1 text-sm text-fg2">
              Use the <span className="font-mono font-medium text-fg">{github.name}</span> connection for ghcr.io. Its
              token needs read:packages.
            </span>
            <Button size="sm" loading={fromGit.isPending} onClick={() => fromGit.mutate(github.id)}>
              Use for ghcr.io
            </Button>
          </div>
        )}

        <form
          id="registry"
          className="flex flex-wrap items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <Field label="Registry" className="w-44">
            <Input required value={host} onChange={(e) => setHost(e.target.value)} placeholder="ghcr.io" className="font-mono" />
          </Field>
          <Field label="Username" className="w-44">
            <Input required value={username} onChange={(e) => setUsername(e.target.value)} placeholder="your-github-user" />
          </Field>
          <Field label="Token" className="min-w-[200px] flex-1">
            <Input required type="password" autoComplete="off" value={secret} onChange={(e) => setSecret(e.target.value)} placeholder="read:packages token" />
          </Field>
          <Button type="submit" variant="primary" loading={save.isPending}>
            Save and sign in
          </Button>
        </form>
        <FormError error={save.error} fallback="Failed" />
      </div>

      {creds.data && creds.data.length > 0 ? (
        <ul className="divide-y divide-hairline border-t border-hairline">
          {creds.data.map((c) => (
            <ListRow
              key={c.id}
              removeLabel={`Remove ${c.host}`}
              onRemove={async () => {
                if (
                  await confirm({
                    title: `Remove the ${c.host} credential?`,
                    body: "Windlass logs the host out of this registry. Private images from it will fail to pull.",
                    confirmLabel: "Remove credential",
                  })
                )
                  remove.mutate(c.id);
              }}
            >
              <Icon name="package" size={16} className="text-fg2" />
              <span className="font-mono font-medium text-fg">{c.host}</span>
              <span className="text-fg3">{c.username}</span>
              {c.verified_at ? <StatusPill tone="ok">Signed in</StatusPill> : <StatusPill tone="err">Never signed in</StatusPill>}
            </ListRow>
          ))}
        </ul>
      ) : (
        creds.data && (
          <CardFooter
            note={
              <>
                Nothing configured. A project pulling a private image fails with <span className="font-mono">unauthorized</span> until one
                is added.
              </>
            }
          />
        )
      )}
    </Card>
  );
}

// ---------- Users ----------

interface AdminUser {
  id: number;
  email: string;
  role: string;
  totp_enabled: boolean;
  oauth: string;
  disabled: boolean;
}

function UsersSection() {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const can = useCan();
  const me = useMe();
  const users = useQuery<AdminUser[]>({ queryKey: ["users"], queryFn: () => api("/users"), retry: false, enabled: can("admin") });
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState("member");

  const create = useMutation({
    mutationFn: () => api("/users", { method: "POST", body: JSON.stringify({ email, password, role }) }),
    onSuccess: () => {
      toast.ok(`Added ${email}`);
      setEmail("");
      setPassword("");
      qc.invalidateQueries({ queryKey: ["users"] });
    },
  });
  const remove = useMutation({
    mutationFn: (id: number) => api(`/users/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
    onError: (e) => toast.err("Could not remove the user", errorText(e)),
  });

  if (!can("admin") || users.isError) return null;

  return (
    <Card>
      <CardHeader
        title="Users"
        description="Viewers can look but not change anything. Members deploy and edit projects. Admins also manage users and server settings."
      />
      <form
        className="flex flex-wrap items-end gap-3 px-5 py-4"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <Field label="Email" className="min-w-[200px] flex-1">
          <Input required type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
        </Field>
        <Field label="Password" className="w-48">
          <Input
            type="password"
            minLength={10}
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Empty: OAuth only"
          />
        </Field>
        <Field label="Role" className="w-32">
          <Select value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="viewer">Viewer</option>
            <option value="member">Member</option>
            <option value="admin">Admin</option>
          </Select>
        </Field>
        <Button type="submit" variant="primary" icon="plus" loading={create.isPending}>
          Add user
        </Button>
        <div className="basis-full empty:hidden">
          <FormError error={create.error} fallback="Failed" />
        </div>
      </form>
      {users.data && users.data.length > 0 && (
        <ul className="divide-y divide-hairline border-t border-hairline">
          {users.data.map((u) => (
            <ListRow
              key={u.id}
              removeLabel={`Remove ${u.email}`}
              onRemove={
                u.id === me.data?.id
                  ? undefined
                  : async () => {
                      if (
                        await confirm({
                          title: `Remove ${u.email}?`,
                          body: "They are signed out everywhere and can no longer reach this panel.",
                          confirmLabel: "Remove user",
                        })
                      )
                        remove.mutate(u.id);
                    }
              }
            >
              <span className="grid h-7 w-7 place-items-center rounded-full border border-hairline bg-sunken text-2xs font-bold uppercase text-fg2">
                {u.email.slice(0, 2)}
              </span>
              <span className="font-medium text-fg">{u.email}</span>
              {u.id === me.data?.id && <span className="text-xs text-fg3">(you)</span>}
              <Tag tone={u.role === "admin" ? "accent" : "idle"}>{u.role}</Tag>
              {u.totp_enabled && <Tag tone="ok">2FA</Tag>}
              {u.oauth && <Tag>{u.oauth}</Tag>}
              {u.disabled && <Tag tone="err">Disabled</Tag>}
            </ListRow>
          ))}
        </ul>
      )}
    </Card>
  );
}

// ---------- Docker storage ----------

interface ImageDiskUsage {
  total_count: number;
  active_count: number;
  total_bytes: number;
  reclaimable_bytes: number;
}

function DockerStorageSection() {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const usage = useQuery<ImageDiskUsage>({ queryKey: ["system", "docker", "images"], queryFn: () => api("/system/docker/images"), retry: false });
  const prune = useMutation<{ deleted: number; reclaimed_bytes: number }>({
    mutationFn: () => api("/system/docker/images/prune", { method: "POST", body: JSON.stringify({ retention_days: 7, keep_deployments: 5 }) }),
    onSuccess: (r) => {
      toast.ok(`Removed ${r.deleted} images`, `${formatBytes(r.reclaimed_bytes)} reclaimed.`);
      qc.invalidateQueries({ queryKey: ["system", "docker", "images"] });
    },
    onError: (e) => toast.err("Cleanup failed", errorText(e)),
  });

  if (usage.isError) return null;
  const u = usage.data;
  return (
    <Card>
      <CardHeader title="Docker image storage" description="Old images pile up with every build and pull." />
      <div className="grid grid-cols-2 divide-x divide-hairline border-b border-hairline sm:grid-cols-3">
        {[
          ["Images", u ? `${u.total_count}` : "…", u ? `${u.active_count} in use` : ""],
          ["Total size", u ? formatBytes(u.total_bytes) : "…", ""],
          ["Reclaimable", u ? formatBytes(u.reclaimable_bytes) : "…", "potentially"],
        ].map(([label, value, sub]) => (
          <div key={label} className="px-5 py-4">
            <div className="text-xs text-fg3">{label}</div>
            <div className="mt-1 text-xl font-semibold tabular-nums tracking-[-0.02em] text-fg">{value}</div>
            {sub && <div className="text-xs text-fg3">{sub}</div>}
          </div>
        ))}
      </div>
      <CardFooter note="Removes unused images older than 7 days, keeping those behind each project's last 5 successful deployments so rollback still works.">
        <Button
          size="sm"
          loading={prune.isPending}
          onClick={async () => {
            if (
              await confirm({
                title: "Clean unused images?",
                body: "Unused images older than 7 days are removed. The images behind each project's last 5 successful deployments are kept.",
                confirmLabel: "Clean images",
              })
            )
              prune.mutate();
          }}
        >
          Clean unused images
        </Button>
      </CardFooter>
    </Card>
  );
}

// ---------- Updates ----------

function UpdateSection() {
  const can = useCan();
  const check = useUpdateCheck(can("admin"));
  const apply = useMutation({ mutationFn: () => api("/system/update", { method: "POST" }) });

  // Arriving from the sidebar update alert (#updates) briefly highlights
  // this card so it's obvious where the click landed.
  const location = useLocation();
  const [flash, setFlash] = useState(false);
  useEffect(() => {
    if (location.hash === "#updates") {
      setFlash(true);
      const t = setTimeout(() => setFlash(false), 2000);
      return () => clearTimeout(t);
    }
  }, [location.hash]);

  if (!can("admin") || check.isError) return null;

  return (
    <div id="updates" className={cn("rounded-card transition-shadow duration-500", flash && "ring-2 ring-[var(--color-accent-fill)] ring-offset-2 ring-offset-panel")}>
      <Card>
        <CardHeader title="Software updates" description="Updating restarts the panel only. Deployed apps keep running." />
        <Row
          title={`Running ${check.data?.current_version ?? "…"}`}
          desc={check.data?.update_available ? `Version ${check.data.version} is available.` : "You're up to date."}
        >
          {check.data?.update_available ? (
            <>
              <StatusPill tone="warn">Update available</StatusPill>
              <Button size="sm" variant="primary" icon="download" onClick={() => apply.mutate()} loading={apply.isPending}>
                Update now
              </Button>
            </>
          ) : (
            check.data && <StatusPill tone="ok">Up to date</StatusPill>
          )}
        </Row>
        {(apply.isSuccess || apply.isError) && (
          <div className="px-5 pb-4">
            {apply.isSuccess && (
              <Callout tone="ok">Updating. The panel restarts in a few seconds. Deployed apps are unaffected.</Callout>
            )}
            {apply.isError && <Callout tone="err">{errorText(apply.error, "Update failed")}</Callout>}
          </div>
        )}
      </Card>
    </div>
  );
}
