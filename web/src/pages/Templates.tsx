import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../api/client";

interface Template {
  key: string;
  name: string;
  description: string;
  default_port: number;
}

export default function Templates() {
  const templates = useQuery<Template[]>({
    queryKey: ["templates"],
    queryFn: () => api("/templates"),
  });
  const navigate = useNavigate();
  const [selected, setSelected] = useState<string | null>(null);
  const [name, setName] = useState("");

  const create = useMutation({
    mutationFn: (key: string) =>
      api<{ project: { name: string } }>(`/templates/${key}`, {
        method: "POST",
        body: JSON.stringify({ name }),
      }),
    onSuccess: (data) =>
      navigate(`/projects/${data.project.name}/deployments`),
  });

  return (
    <div>
      <h1 className="text-xl font-semibold">Templates</h1>
      <p className="mt-1 text-sm text-zinc-500">
        One-click databases. Each becomes an ordinary compose project with
        generated credentials in its Environment tab — no proprietary formats.
      </p>

      <div className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {templates.data?.map((t) => (
          <div
            key={t.key}
            className="rounded-lg border border-zinc-900 bg-zinc-900/50 p-4"
          >
            <div className="font-medium">{t.name}</div>
            <p className="mt-1 text-xs text-zinc-500">{t.description}</p>
            {selected === t.key ? (
              <form
                className="mt-3 space-y-2"
                onSubmit={(e) => {
                  e.preventDefault();
                  create.mutate(t.key);
                }}
              >
                <input
                  autoFocus
                  required
                  value={name}
                  onChange={(e) => setName(e.target.value.toLowerCase())}
                  placeholder="project name"
                  pattern="[a-z0-9][a-z0-9_-]*"
                  className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
                />
                <button
                  type="submit"
                  disabled={create.isPending}
                  className="w-full rounded-md bg-zinc-100 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
                >
                  {create.isPending ? "Creating…" : "Create & deploy"}
                </button>
                {create.isError && (
                  <p className="text-xs text-red-400">
                    {create.error instanceof Error ? create.error.message : "Failed"}
                  </p>
                )}
              </form>
            ) : (
              <button
                onClick={() => {
                  setSelected(t.key);
                  setName(t.key);
                }}
                className="mt-3 w-full rounded-md border border-zinc-700 py-1.5 text-sm hover:bg-zinc-900"
              >
                Create
              </button>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
