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
  const [secret, setSecret] = useState<string | null>(null);
  const [initialized, setInitialized] = useState(false);

  if (proj.data && !initialized) {
    setRepo(proj.data.git_repo ?? "");
    setBranch(proj.data.git_branch ?? "main");
    setAutoDeploy(proj.data.auto_deploy);
    setInitialized(true);
  }

  const save = useMutation({
    mutationFn: () =>
      api<{ webhook_secret: string }>(`/projects/${project}/git`, {
        method: "PUT",
        body: JSON.stringify({
          repo,
          branch,
          auto_deploy: autoDeploy,
          connection_id: connectionId || undefined,
        }),
      }),
    onSuccess: (data) => {
      setSecret(data.webhook_secret);
      qc.invalidateQueries({ queryKey: ["projects", project] });
    },
  });

  const webhookUrl = `${window.location.origin}/api/v1/webhooks/github/${project}`;

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
        <Field label="Repository (https clone URL)">
          <Input
            required
            value={repo}
            onChange={(e) => setRepo(e.target.value)}
            placeholder="https://github.com/acme/app.git"
          />
        </Field>

        <div className="flex gap-3">
          <Field label="Branch" className="flex-1">
            <Input
              required
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
            />
          </Field>
          <Field label="Connection (private repos)" className="flex-1">
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

        <Button type="submit" variant="primary" disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save git settings"}
        </Button>
        {save.isError && (
          <p className="text-sm text-err">
            {save.error instanceof Error ? save.error.message : "Save failed"}
          </p>
        )}
      </form>

      {secret && (
        <div className="mt-6 rounded-[10px] bg-ok-soft px-4 py-3.5 text-sm text-ok">
          <p className="font-semibold">Add this webhook to your repository</p>
          <dl className="mt-3 space-y-2">
            <div>
              <dt className="text-xs font-semibold text-fg3">Payload URL (GitHub) — use /gitlab/ for GitLab</dt>
              <dd className="font-mono text-xs text-fg2 break-all">{webhookUrl}</dd>
            </div>
            <div>
              <dt className="text-xs font-semibold text-fg3">
                Secret (shown once — GitHub: webhook secret · GitLab: secret token)
              </dt>
              <dd className="font-mono text-xs text-fg2 break-all">{secret}</dd>
            </div>
            <div>
              <dt className="text-xs font-semibold text-fg3">Content type</dt>
              <dd className="font-mono text-xs text-fg2 break-all">application/json</dd>
            </div>
          </dl>
        </div>
      )}
    </div>
  );
}
