import { useEffect, useState, type ReactNode } from "react";
import { Link, Navigate, useLocation, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { Page } from "../ui/Page";
import { Card } from "../ui/Card";
import { Button, btn } from "../ui/Button";
import { Input, Select, Field } from "../ui/Field";
import { StatusPill, Chip } from "../ui/Badge";
import { Icon } from "../ui/Icon";
import { Segmented } from "../ui/Segmented";
import { ThemeToggle } from "../ui/ThemeToggle";
import { cn } from "../ui/cn";

interface Connection {
  id: number;
  provider: string;
  name: string;
}

// Each tab is a real route (/settings/<tab>) so sections are addressable:
// the sidebar update alert links to system, and OAuth redirects land on git.
const TABS = [
  { value: "general", label: "General" },
  { value: "auth", label: "Users & auth" },
  { value: "git", label: "Git" },
  { value: "registries", label: "Registries" },
  { value: "system", label: "System" },
] as const;

type Tab = (typeof TABS)[number]["value"];

export default function Settings() {
  const { tab } = useParams();
  const navigate = useNavigate();

  if (!TABS.some((t) => t.value === tab)) {
    return <Navigate to="/settings/general" replace />;
  }

  return (
    <Page title="Settings">
      <div className="w-full">
        <Segmented
          className="mb-6 w-full"
          options={[...TABS]}
          value={tab as Tab}
          onChange={(v) => navigate(`/settings/${v}`)}
        />
        {tab === "general" && (
          <>
            <AppearanceSection />
            <PanelDomainSection />
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
    </Page>
  );
}

// ---------- Layout helpers ----------

function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="mb-7">
      <div className="mb-2 px-1 text-xs font-semibold uppercase tracking-[0.03em] text-fg3">{title}</div>
      <Card className="overflow-hidden p-0">
        <div className="divide-y divide-hairline">{children}</div>
      </Card>
    </section>
  );
}

function Row({
  title,
  desc,
  children,
  stack,
}: {
  title?: ReactNode;
  desc?: ReactNode;
  children?: ReactNode;
  stack?: boolean;
}) {
  return (
    <div className={cn("flex gap-4 px-5 py-4", stack ? "flex-col items-stretch" : "items-center")}>
      {(title || desc) && (
        <div className="min-w-0 flex-1">
          {title && <div className="text-md font-semibold">{title}</div>}
          {desc && <div className="mt-0.5 text-sm leading-normal text-fg3">{desc}</div>}
        </div>
      )}
      {children && <div className={cn(stack ? "" : "flex flex-none items-center gap-2.5")}>{children}</div>}
    </div>
  );
}

function Notice({ tone, children, onClose }: { tone: "ok" | "err"; children: ReactNode; onClose?: () => void }) {
  return (
    <div
      className={cn(
        "flex items-center gap-2 rounded-[10px] px-3.5 py-2.5 text-sm",
        tone === "ok" ? "bg-ok-soft text-ok" : "bg-err-soft text-err",
      )}
    >
      <span className="flex-1">{children}</span>
      {onClose && (
        <button onClick={onClose} className="opacity-70 hover:opacity-100" aria-label="Dismiss">
          <Icon name="x" size={15} />
        </button>
      )}
    </div>
  );
}

// ---------- Appearance ----------

function AppearanceSection() {
  return (
    <Group title="Appearance">
      <Row title="Theme" desc="Auto matches your system between light and dark automatically.">
        <ThemeToggle />
      </Row>
    </Group>
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
    onSettled: () => qc.invalidateQueries({ queryKey: ["system", "panel-domain"] }),
  });

  if (status.isError) return null;
  return (
    <Group title="Panel domain">
      <Row
        title="Hostname"
        desc="Point this name's DNS A/AAAA record at the server; Windlass adds its own Caddy route and gets HTTPS automatically."
        stack
      >
        <div className="mt-1 flex gap-2">
          <Input
            value={hostname}
            onChange={(e) => setHostname(e.target.value.toLowerCase())}
            placeholder="windlass.example.com"
          />
          <Button variant="primary" onClick={() => save.mutate(hostname.trim())} disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
        </div>
        {status.data?.configured && (
          <div className="mt-3 flex flex-wrap items-center gap-3">
            <StatusPill tone={status.data.proxy_available ? "ok" : "warn"}>
              {status.data.proxy_available ? "Active" : "Caddy unavailable"}
            </StatusPill>
            <a className="font-mono text-sm text-accent hover:underline" href={status.data.url}>
              {status.data.url}
            </a>
            <button
              className="text-xs text-fg3 hover:text-err"
              onClick={() => { setHostname(""); save.mutate(""); }}
            >
              Remove
            </button>
          </div>
        )}
        {save.isError && (
          <p className="mt-2 text-sm text-err">
            {save.error instanceof Error ? save.error.message : "Could not configure panel domain"}
          </p>
        )}
      </Row>
    </Group>
  );
}

// ---------- Two-factor ----------

function SecuritySection() {
  const [enroll, setEnroll] = useState<{ secret: string; otpauth_url: string } | null>(null);
  const [code, setCode] = useState("");
  const qc = useQueryClient();
  const me = useQuery<{ totp_enabled: boolean }>({ queryKey: ["auth", "me"], queryFn: () => api("/auth/me") });

  const begin = useMutation({
    mutationFn: () => api<{ secret: string; otpauth_url: string }>("/auth/totp/setup", { method: "POST" }),
    onSuccess: setEnroll,
  });
  const verify = useMutation({
    mutationFn: () => api("/auth/totp/verify", { method: "POST", body: JSON.stringify({ code }) }),
    onSuccess: () => { setEnroll(null); setCode(""); qc.invalidateQueries({ queryKey: ["auth", "me"] }); },
  });
  const disable = useMutation({
    mutationFn: () => api("/auth/totp/disable", { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["auth", "me"] }),
  });

  return (
    <Group title="Two-factor authentication">
      {me.data?.totp_enabled ? (
        <Row title="Authenticator app" desc="An authenticator code is required at sign-in.">
          <StatusPill tone="ok">Enabled</StatusPill>
          <Button size="sm" variant="ghost" onClick={() => disable.mutate()}>Disable</Button>
        </Row>
      ) : enroll ? (
        <Row title="Set up authenticator" stack>
          <p className="text-sm text-fg2">Add this secret to your authenticator app, then confirm a code:</p>
          <code className="mt-2 block break-all rounded-[8px] bg-sunken p-2.5 font-mono text-xs">{enroll.secret}</code>
          <code className="mt-2 block break-all rounded-[8px] bg-sunken p-2.5 font-mono text-xs text-fg3">{enroll.otpauth_url}</code>
          <div className="mt-3 flex gap-2">
            <Input
              inputMode="numeric"
              maxLength={6}
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
              placeholder="123456"
              className="w-36 text-center font-mono tracking-[0.3em]"
            />
            <Button variant="primary" onClick={() => verify.mutate()} disabled={code.length !== 6 || verify.isPending}>
              Confirm
            </Button>
          </div>
          {verify.isError && (
            <p className="mt-2 text-sm text-err">
              {verify.error instanceof Error ? verify.error.message : "Invalid code"}
            </p>
          )}
        </Row>
      ) : (
        <Row title="Authenticator app" desc="Add a second factor with any TOTP authenticator.">
          <Button size="sm" onClick={() => begin.mutate()}>Enable TOTP</Button>
        </Row>
      )}
    </Group>
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
  state_mismatch: "The creation state did not match — try again.",
  missing_code: "GitHub did not return a creation code — try again.",
  conversion_failed: "GitHub rejected the app manifest exchange — try again.",
};

function GitHubAppSection() {
  const me = useQuery<{ role: string }>({ queryKey: ["auth", "me"], queryFn: () => api("/auth/me") });
  const app = useQuery<GitHubAppStatus>({
    queryKey: ["system", "github-app"],
    queryFn: () => api("/system/github-app"),
    retry: false,
  });

  const [searchParams, setSearchParams] = useSearchParams();
  const created = searchParams.get("github_app");
  const appError = searchParams.get("github_app_error");

  if (me.data?.role !== "admin" || app.isError) return null;

  return (
    <Group title="GitHub App">
      <Row
        title="Connect GitHub in two clicks"
        desc="Windlass sends GitHub a pre-filled app manifest; you confirm once and the credentials come back automatically — repository access and push auto-deploys, no copying."
        stack
      >
        {created && (
          <div className="mb-3">
            <Notice tone="ok" onClose={() => setSearchParams({}, { replace: true })}>
              GitHub App <span className="font-mono">{created}</span> created. Install it on your
              repositories from{" "}
              <Link to="/settings/git" className="underline">
                Settings → Git
              </Link>
              . To also sign in with GitHub, add the “Email addresses: read” account permission on
              GitHub — manifests cannot request it.
            </Notice>
          </div>
        )}
        {appError && (
          <div className="mb-3">
            <Notice tone="err" onClose={() => setSearchParams({}, { replace: true })}>
              {githubAppErrors[appError] ?? "GitHub App creation failed."}
            </Notice>
          </div>
        )}

        {app.data?.configured ? (
          <div className="flex flex-wrap items-center gap-3">
            <StatusPill tone="ok">Configured</StatusPill>
            <span className="text-sm">
              <span className="font-mono font-medium">{app.data.slug}</span>
              {app.data.owner && <span className="text-fg3"> · owned by {app.data.owner}</span>}
            </span>
            {app.data.html_url && (
              <a
                href={app.data.html_url}
                target="_blank"
                rel="noreferrer"
                className="text-sm text-accent hover:underline"
              >
                Manage on GitHub
              </a>
            )}
            <Link to="/settings/git" className="text-sm text-accent hover:underline">
              Install on repositories
            </Link>
          </div>
        ) : (
          <div className="flex items-center gap-2.5">
            <a href="/api/v1/system/github-app/create" className={btn("primary", "md")}>
              <Icon name="github" size={16} /> Create GitHub App
            </a>
          </div>
        )}
      </Row>
    </Group>
  );
}

// ---------- OAuth applications ----------

function OAuthAppsSection() {
  const qc = useQueryClient();
  const me = useQuery<{ role: string }>({ queryKey: ["auth", "me"], queryFn: () => api("/auth/me") });
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
    onSuccess: () => { setClientId(""); setClientSecret(""); qc.invalidateQueries({ queryKey: ["auth", "oauth-providers"] }); },
  });

  if (me.data?.role !== "admin") return null;
  const callbackUrl = `${window.location.origin}/api/v1/auth/oauth/${provider}/callback`;

  return (
    <Group title="OAuth applications">
      <Row
        title="Connect an identity provider"
        desc="Manual setup: register an app with this callback URL, then paste its credentials. Needed for Google sign-in; for GitHub, prefer the two-click GitHub App above."
        stack
      >
        <Chip className="mt-1 self-start break-all">{callbackUrl}</Chip>
        <form
          className="mt-3 flex flex-wrap items-end gap-2.5"
          onSubmit={(e) => { e.preventDefault(); save.mutate(); }}
        >
          <Field label="Provider" className="w-[130px]">
            <Select value={provider} onChange={(e) => setProvider(e.target.value)}>
              <option value="github">GitHub</option>
              <option value="google">Google</option>
            </Select>
          </Field>
          <Field label="Client ID" className="min-w-[160px] flex-1">
            <Input required value={clientId} onChange={(e) => setClientId(e.target.value)} className="font-mono" />
          </Field>
          <Field label="Client secret" className="min-w-[160px] flex-1">
            <Input required type="password" value={clientSecret} onChange={(e) => setClientSecret(e.target.value)} />
          </Field>
          <Button type="submit" variant="primary" disabled={save.isPending}>Save</Button>
        </form>
        <div className="mt-3 flex gap-4 text-xs text-fg3">
          <span>GitHub · {providers.data?.github ? <span className="text-ok">configured</span> : "not configured"}</span>
          <span>Google · {providers.data?.google ? <span className="text-ok">configured</span> : "not configured"}</span>
        </div>
        {save.isError && (
          <p className="mt-2 text-sm text-err">
            {save.error instanceof Error ? save.error.message : "Failed to save"}
          </p>
        )}
      </Row>
    </Group>
  );
}

// ---------- Git connections ----------

const gitErrorMessages: Record<string, string> = {
  not_configured: "The GitHub OAuth app is not configured.",
  state_mismatch: "The authorization state did not match — try connecting again.",
  exchange_failed: "GitHub rejected the authorization code — try connecting again.",
  profile_failed: "Connected, but the GitHub profile could not be read.",
  app_install_failed: "The GitHub App installation could not be linked — try again.",
};

function GitConnections() {
  const qc = useQueryClient();
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
    onSuccess: () => { setName(""); setToken(""); setManualOpen(false); qc.invalidateQueries({ queryKey: ["git", "connections"] }); },
  });
  const remove = useMutation({
    mutationFn: (id: number) => api(`/git/connections/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["git", "connections"] }),
  });

  return (
    <Group title="Git connections">
      <Row
        title="Private repository access"
        desc="Tokens are stored encrypted and never written to disk."
        stack
      >
        {connected && (
          <div className="mb-3">
            <Notice tone="ok" onClose={() => setSearchParams({}, { replace: true })}>
              GitHub account connected as <span className="font-mono">{connected}</span>.
            </Notice>
          </div>
        )}
        {gitError && (
          <div className="mb-3">
            <Notice tone="err" onClose={() => setSearchParams({}, { replace: true })}>
              {gitErrorMessages[gitError] ?? "GitHub connect failed."}
            </Notice>
          </div>
        )}

        <div className="flex flex-wrap items-center gap-2.5">
          {app.data?.configured ? (
            <a
              href={`https://github.com/apps/${app.data.slug}/installations/new`}
              className={btn("primary", "md")}
            >
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
                Users &amp; auth
              </Link>{" "}
              for two-click connect.
            </p>
          )}
          <Button variant="ghost" onClick={() => setManualOpen((o) => !o)}>
            {manualOpen ? "Cancel manual token" : "Add a token manually"}
          </Button>
        </div>

        {manualOpen && (
          <form
            className="mt-4 flex flex-wrap items-end gap-2.5"
            onSubmit={(e) => { e.preventDefault(); add.mutate(); }}
          >
            <Field label="Provider" className="w-[120px]">
              <Select value={provider} onChange={(e) => setProvider(e.target.value)}>
                <option value="github">GitHub</option>
                <option value="gitlab">GitLab</option>
              </Select>
            </Field>
            <Field label="Name" className="w-[150px]">
              <Input required value={name} onChange={(e) => setName(e.target.value)} placeholder="acme-bot" />
            </Field>
            <Field label="Token" className="min-w-[160px] flex-1">
              <Input required type="password" value={token} onChange={(e) => setToken(e.target.value)} placeholder="ghp_… / glpat-…" />
            </Field>
            <Button type="submit" variant="primary" disabled={add.isPending}>Add</Button>
          </form>
        )}
        {add.isError && (
          <p className="mt-2 text-sm text-err">{add.error instanceof Error ? add.error.message : "Failed"}</p>
        )}

        {connections.data && connections.data.length > 0 && (
          <div className="mt-4 space-y-2">
            {connections.data.map((c) => (
              <div key={c.id} className="flex items-center justify-between rounded-[10px] border border-hairline bg-surface2 px-4 py-2.5">
                <div className="flex items-center gap-2.5 text-sm">
                  <span className="font-mono font-medium">{c.name}</span>
                  <StatusPill tone="idle">{c.provider}</StatusPill>
                </div>
                <button onClick={() => remove.mutate(c.id)} className="text-xs text-fg3 hover:text-err">Remove</button>
              </div>
            ))}
          </div>
        )}
      </Row>
    </Group>
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
      qc.invalidateQueries({ queryKey: ["registries"] });
    },
  });
  const remove = useMutation({
    mutationFn: (id: number) => api(`/registries/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["registries"] }),
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
    <Group title="Container registries">
      <Row
        title="Private image access"
        desc="Applied to the host with docker login, so pulls keep working if Windlass is stopped or removed. Tokens are stored encrypted and never returned."
        stack
      >
        {warning && (
          <div className="mb-3">
            <Notice tone="err" onClose={() => setWarning(null)}>
              Saved, but signing in failed: {warning}
            </Notice>
          </div>
        )}

        {github && (
          <div className="mb-4 flex flex-wrap items-center gap-2.5">
            <Button
              variant="primary"
              disabled={fromGit.isPending}
              onClick={() => fromGit.mutate(github.id)}
            >
              <Icon name="github" size={16} />
              {fromGit.isPending ? "Signing in…" : `Use ${github.name} for ghcr.io`}
            </Button>
            <span className="text-sm text-fg3">
              Uses the GitHub connection you already have. Needs read:packages on that token.
            </span>
          </div>
        )}

        <form
          className="flex flex-wrap items-end gap-2.5"
          onSubmit={(e) => { e.preventDefault(); save.mutate(); }}
        >
          <Field label="Registry" className="w-[170px]">
            <Input required value={host} onChange={(e) => setHost(e.target.value)} placeholder="ghcr.io" />
          </Field>
          <Field label="Username" className="w-[150px]">
            <Input required value={username} onChange={(e) => setUsername(e.target.value)} placeholder="your-github-user" />
          </Field>
          <Field label="Token" className="min-w-[160px] flex-1">
            <Input required type="password" value={secret} onChange={(e) => setSecret(e.target.value)} placeholder="read:packages token" />
          </Field>
          <Button type="submit" variant="primary" disabled={save.isPending}>
            {save.isPending ? "Signing in…" : "Save and sign in"}
          </Button>
        </form>
        {save.isError && (
          <p className="mt-2 text-sm text-err">{save.error instanceof Error ? save.error.message : "Failed"}</p>
        )}

        {creds.data && creds.data.length > 0 && (
          <div className="mt-4 space-y-2">
            {creds.data.map((c) => (
              <div key={c.id} className="flex items-center justify-between rounded-[10px] border border-hairline bg-surface2 px-4 py-2.5">
                <div className="flex items-center gap-2.5 text-sm">
                  <span className="font-mono font-medium">{c.host}</span>
                  <span className="text-fg3">{c.username}</span>
                  {c.verified_at ? (
                    <StatusPill tone="ok">signed in</StatusPill>
                  ) : (
                    <StatusPill tone="err">never signed in</StatusPill>
                  )}
                </div>
                <button onClick={() => remove.mutate(c.id)} className="text-xs text-fg3 hover:text-err">Remove</button>
              </div>
            ))}
          </div>
        )}
        {creds.data && creds.data.length === 0 && (
          <p className="mt-3 text-sm text-fg3">
            Nothing configured. A project pulling a private image will fail with
            <span className="font-mono"> unauthorized</span> until one is added.
          </p>
        )}
      </Row>
    </Group>
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
  const users = useQuery<AdminUser[]>({ queryKey: ["users"], queryFn: () => api("/users"), retry: false });
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState("member");

  const create = useMutation({
    mutationFn: () => api("/users", { method: "POST", body: JSON.stringify({ email, password, role }) }),
    onSuccess: () => { setEmail(""); setPassword(""); qc.invalidateQueries({ queryKey: ["users"] }); },
  });
  const remove = useMutation({
    mutationFn: (id: number) => api(`/users/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
  });

  if (users.isError) return null;

  return (
    <Group title="Users">
      <Row stack>
        <form className="flex flex-wrap items-end gap-2.5" onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
          <Field label="Email" className="min-w-[180px] flex-1">
            <Input required type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          </Field>
          <Field label="Password (min 10)" className="w-[180px]">
            <Input type="password" minLength={10} value={password} onChange={(e) => setPassword(e.target.value)} placeholder="empty = OAuth-only" />
          </Field>
          <Field label="Role" className="w-[130px]">
            <Select value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="viewer">viewer</option>
              <option value="member">member</option>
              <option value="admin">admin</option>
            </Select>
          </Field>
          <Button type="submit" variant="primary" disabled={create.isPending}>Add</Button>
        </form>
        {create.isError && (
          <p className="mt-2 text-sm text-err">{create.error instanceof Error ? create.error.message : "Failed"}</p>
        )}
        {users.data && users.data.length > 0 && (
          <div className="mt-4 space-y-2">
            {users.data.map((u) => (
              <div key={u.id} className="flex items-center justify-between rounded-[10px] border border-hairline bg-surface2 px-4 py-2.5 text-sm">
                <div className="flex items-center gap-2.5">
                  <span className="font-medium">{u.email}</span>
                  <StatusPill tone={u.role === "admin" ? "accent" : "idle"}>{u.role}</StatusPill>
                  {u.totp_enabled && <span className="text-xs text-fg3">2FA</span>}
                </div>
                <button
                  onClick={() => { if (confirm(`Delete user ${u.email}?`)) remove.mutate(u.id); }}
                  className="text-xs text-fg3 hover:text-err"
                >
                  Remove
                </button>
              </div>
            ))}
          </div>
        )}
      </Row>
    </Group>
  );
}

// ---------- Docker storage ----------

interface ImageDiskUsage {
  total_count: number;
  active_count: number;
  total_bytes: number;
  reclaimable_bytes: number;
}

function formatBytes(bytes: number) {
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KiB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GiB`;
}

function DockerStorageSection() {
  const qc = useQueryClient();
  const usage = useQuery<ImageDiskUsage>({ queryKey: ["system", "docker", "images"], queryFn: () => api("/system/docker/images"), retry: false });
  const prune = useMutation<{ deleted: number; reclaimed_bytes: number }>({
    mutationFn: () => api("/system/docker/images/prune", { method: "POST", body: JSON.stringify({ retention_days: 7, keep_deployments: 5 }) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["system", "docker", "images"] }),
  });

  if (usage.isError) return null;
  return (
    <Group title="Docker image storage">
      <Row
        title="Reclaim disk space"
        desc={
          usage.data
            ? `${usage.data.total_count} images use ${formatBytes(usage.data.total_bytes)}; ${formatBytes(usage.data.reclaimable_bytes)} potentially reclaimable.`
            : "Calculating image usage…"
        }
        stack
      >
        <div className="mt-1">
          <Button
            size="sm"
            onClick={() => {
              if (confirm("Remove unused images older than 7 days while preserving the last 5 successful deployments per project?")) prune.mutate();
            }}
            disabled={prune.isPending}
          >
            {prune.isPending ? "Cleaning…" : "Clean unused images"}
          </Button>
        </div>
        {prune.data && (
          <p className="mt-2 text-sm text-ok">Removed {prune.data.deleted} images ({formatBytes(prune.data.reclaimed_bytes)}).</p>
        )}
        {prune.isError && (
          <p className="mt-2 text-sm text-err">{prune.error instanceof Error ? prune.error.message : "Cleanup failed"}</p>
        )}
      </Row>
    </Group>
  );
}

// ---------- Updates ----------

interface UpdateInfo {
  version: string;
  current_version: string;
  update_available: boolean;
}

function UpdateSection() {
  const check = useQuery<UpdateInfo>({ queryKey: ["system", "update"], queryFn: () => api("/system/update"), retry: false });
  const apply = useMutation({ mutationFn: () => api("/system/update", { method: "POST" }) });

  // Arriving from the sidebar update alert (#updates) briefly highlights
  // this group so it's obvious where the click landed.
  const location = useLocation();
  const [flash, setFlash] = useState(false);
  useEffect(() => {
    if (location.hash === "#updates") {
      setFlash(true);
      const t = setTimeout(() => setFlash(false), 2000);
      return () => clearTimeout(t);
    }
  }, [location.hash]);

  if (check.isError) return null;

  return (
    <div
      id="updates"
      className={cn(
        "rounded-[16px] transition-shadow duration-500",
        flash && "ring-2 ring-[var(--color-accent-fill)]",
      )}
    >
      <Group title="Software updates">
      <Row
        title={`Running ${check.data?.current_version ?? "…"}`}
        desc={check.data?.update_available ? `Version ${check.data.version} is available.` : "You're up to date."}
      >
        {check.data?.update_available ? (
          <>
            <StatusPill tone="warn">Update available</StatusPill>
            <Button size="sm" variant="primary" onClick={() => apply.mutate()} disabled={apply.isPending}>
              {apply.isPending ? "Updating…" : "Update now"}
            </Button>
          </>
        ) : (
          <StatusPill tone="ok">Up to date</StatusPill>
        )}
      </Row>
        {(apply.isSuccess || apply.isError) && (
          <Row stack>
            {apply.isSuccess && (
              <p className="text-sm text-ok">Updating — the panel restarts in a few seconds. Deployed apps are unaffected.</p>
            )}
            {apply.isError && (
              <p className="text-sm text-err">{apply.error instanceof Error ? apply.error.message : "Update failed"}</p>
            )}
          </Row>
        )}
      </Group>
    </div>
  );
}
