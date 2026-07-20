import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { useProject } from "../api/projects";
import { Button } from "../ui/Button";
import { Field, Input, Select } from "../ui/Field";

interface Connection {
  id: number;
  provider: string;
  name: string;
}

interface Repo {
  full_name: string;
  clone_url: string;
  default_branch: string;
  private: boolean;
}

interface SaveResult {
  webhook_secret: string;
  webhook_registered: boolean;
}

const CUSTOM = "__custom";

export default function GitTab({ project }: { project: string }) {
  const qc = useQueryClient();
  const proj = useProject(project);
  const connections = useQuery<Connection[]>({
    queryKey: ["git", "connections"],
    queryFn: () => api("/git/connections"),
  });

  const [repo, setRepo] = useState("");
  const [branch, setBranch] = useState("main");
  const [connectionId, setConnectionId] = useState(0);
  const [autoDeploy, setAutoDeploy] = useState(true);
  const [customUrl, setCustomUrl] = useState(false);
  const [result, setResult] = useState<SaveResult | null>(null);
  const [initialized, setInitialized] = useState(false);

  if (proj.data && !initialized) {
    setRepo(proj.data.git_repo ?? "");
    setBranch(proj.data.git_branch ?? "main");
    setAutoDeploy(proj.data.auto_deploy);
    setInitialized(true);
  }

  const repos = useQuery<Repo[]>({
    queryKey: ["git", "repos", connectionId],
    queryFn: () => api(`/git/connections/${connectionId}/repos`),
    enabled: connectionId > 0,
    staleTime: 60 * 1000,
    retry: false,
  });

  const save = useMutation({
    mutationFn: () =>
      api<SaveResult>(`/projects/${project}/git`, {
        method: "PUT",
        body: JSON.stringify({
          repo,
          branch,
          auto_deploy: autoDeploy,
          connection_id: connectionId || undefined,
        }),
      }),
    onSuccess: (data) => {
      setResult(data);
      qc.invalidateQueries({ queryKey: ["projects", project] });
    },
  });

  const provider =
    connections.data?.find((c) => c.id === connectionId)?.provider ?? "github";
  const webhookUrl = `${window.location.origin}/api/v1/webhooks/${provider}/${project}`;

  // The picker shows once a connection's repos load; a repo URL that is not
  // in the list (or the explicit choice) falls back to the manual URL input.
  const showPicker = connectionId > 0 && (repos.data?.length ?? 0) > 0;
  const matched = repos.data?.some((r) => r.clone_url === repo) ?? false;
  const pickerValue = customUrl || (repo !== "" && !matched) ? CUSTOM : repo;
  const showUrlInput = !showPicker || pickerValue === CUSTOM;

  return (
    <div className="max-w-2xl">
      <p className="text-sm leading-relaxed text-fg2">
        Connect a repository and Windlass deploys on every push to the branch:
        clone/pull → compose pull/build → up. Private repositories need a git
        connection (Settings → Git).
      </p>

      <form
        className="mt-5 space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <Field label="Connection (private repos)">
          <Select
            value={connectionId}
            onChange={(e) => setConnectionId(parseInt(e.target.value, 10))}
          >
            <option value={0}>None (public repo)</option>
            {connections.data?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name} ({c.provider})
              </option>
            ))}
          </Select>
        </Field>

        {showPicker && (
          <Field label="Repository">
            <Select
              value={pickerValue}
              onChange={(e) => {
                const v = e.target.value;
                if (v === CUSTOM) {
                  setCustomUrl(true);
                  return;
                }
                setCustomUrl(false);
                setRepo(v);
                const picked = repos.data?.find((r) => r.clone_url === v);
                if (picked?.default_branch) setBranch(picked.default_branch);
              }}
            >
              <option value="">Choose a repository…</option>
              {repos.data?.map((r) => (
                <option key={r.clone_url} value={r.clone_url}>
                  {r.full_name}
                  {r.private ? " (private)" : ""}
                </option>
              ))}
              <option value={CUSTOM}>Enter URL manually…</option>
            </Select>
          </Field>
        )}
        {connectionId > 0 && repos.isError && (
          <p className="text-xs text-fg3">
            Could not list repositories for this connection — enter the clone
            URL below.
          </p>
        )}

        {showUrlInput && (
          <Field label="Repository (https clone URL)">
            <Input
              required
              value={repo}
              onChange={(e) => setRepo(e.target.value)}
              placeholder="https://github.com/acme/app.git"
            />
          </Field>
        )}

        <div className="flex gap-3">
          <Field label="Branch" className="flex-1">
            <Input
              required
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
            />
          </Field>
        </div>

        <label className="flex items-center gap-2.5 text-sm text-fg">
          <input
            type="checkbox"
            checked={autoDeploy}
            onChange={(e) => setAutoDeploy(e.target.checked)}
            className="h-[18px] w-[18px] flex-none rounded-[6px] accent-[var(--color-accent-fill)]"
          />
          Deploy automatically on push
        </label>

        <Button type="submit" variant="primary" disabled={save.isPending || !repo}>
          {save.isPending ? "Saving…" : "Save git settings"}
        </Button>
        {save.isError && (
          <p className="text-sm text-err">
            {save.error instanceof Error ? save.error.message : "Save failed"}
          </p>
        )}
      </form>

      {result?.webhook_registered && (
        <div className="mt-6 rounded-[10px] bg-ok-soft px-4 py-3.5 text-sm text-ok">
          <p className="font-semibold">Auto-deploy is live</p>
          <p className="mt-1.5">
            The webhook was registered on the repository for you — every push
            to <span className="font-mono text-xs">{branch}</span> now deploys
            automatically. Nothing else to set up.
          </p>
        </div>
      )}

      {result && !result.webhook_registered && (
        <div className="mt-6 rounded-[10px] bg-ok-soft px-4 py-3.5 text-sm text-ok">
          <p className="font-semibold">Add this webhook to your repository</p>
          <dl className="mt-3 space-y-2">
            <div>
              <dt className="text-xs font-semibold text-fg3">
                Payload URL ({provider === "gitlab" ? "GitLab" : "GitHub"})
              </dt>
              <dd className="font-mono text-xs text-fg2 break-all">{webhookUrl}</dd>
            </div>
            <div>
              <dt className="text-xs font-semibold text-fg3">
                Secret (shown once — GitHub: webhook secret · GitLab: secret token)
              </dt>
              <dd className="font-mono text-xs text-fg2 break-all">
                {result.webhook_secret}
              </dd>
            </div>
            <div>
              <dt className="text-xs font-semibold text-fg3">Content type</dt>
              <dd className="font-mono text-xs text-fg2 break-all">
                application/json
              </dd>
            </div>
          </dl>
        </div>
      )}
    </div>
  );
}
