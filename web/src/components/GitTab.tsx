import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { useProject } from "../api/projects";

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
      <p className="text-sm text-zinc-500">
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
        <label className="block">
          <span className="mb-1 block text-xs text-zinc-500">Repository (https clone URL)</span>
          <input
            required
            value={repo}
            onChange={(e) => setRepo(e.target.value)}
            placeholder="https://github.com/acme/app.git"
            className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          />
        </label>

        <div className="flex gap-3">
          <label className="flex-1">
            <span className="mb-1 block text-xs text-zinc-500">Branch</span>
            <input
              required
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
              className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
            />
          </label>
          <label className="flex-1">
            <span className="mb-1 block text-xs text-zinc-500">Connection (private repos)</span>
            <select
              value={connectionId}
              onChange={(e) => setConnectionId(parseInt(e.target.value, 10))}
              className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
            >
              <option value={0}>None (public repo)</option>
              {connections.data?.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name} ({c.provider})
                </option>
              ))}
            </select>
          </label>
        </div>

        <label className="flex items-center gap-2 text-sm text-zinc-300">
          <input
            type="checkbox"
            checked={autoDeploy}
            onChange={(e) => setAutoDeploy(e.target.checked)}
          />
          Deploy automatically on push
        </label>

        <button
          type="submit"
          disabled={save.isPending}
          className="rounded-md bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
        >
          {save.isPending ? "Saving…" : "Save git settings"}
        </button>
        {save.isError && (
          <p className="text-sm text-red-400">
            {save.error instanceof Error ? save.error.message : "Save failed"}
          </p>
        )}
      </form>

      {secret && (
        <div className="mt-6 rounded-md border border-emerald-900/50 bg-emerald-950/20 p-4 text-sm">
          <p className="font-medium text-emerald-300">
            Add this webhook to your repository
          </p>
          <dl className="mt-3 space-y-2 text-zinc-300">
            <div>
              <dt className="text-xs text-zinc-500">Payload URL (GitHub) — use /gitlab/ for GitLab</dt>
              <dd className="font-mono text-xs">{webhookUrl}</dd>
            </div>
            <div>
              <dt className="text-xs text-zinc-500">
                Secret (shown once — GitHub: webhook secret · GitLab: secret token)
              </dt>
              <dd className="font-mono text-xs">{secret}</dd>
            </div>
            <div>
              <dt className="text-xs text-zinc-500">Content type</dt>
              <dd className="font-mono text-xs">application/json</dd>
            </div>
          </dl>
        </div>
      )}
    </div>
  );
}
