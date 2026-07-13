import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";

interface Connection {
  id: number;
  provider: string;
  name: string;
}

export default function Settings() {
  return (
    <div>
      <h1 className="text-xl font-semibold">Settings</h1>
      <div className="mt-6 max-w-2xl">
        <GitConnections />
      </div>
    </div>
  );
}

function GitConnections() {
  const qc = useQueryClient();
  const connections = useQuery<Connection[]>({
    queryKey: ["git", "connections"],
    queryFn: () => api("/git/connections"),
  });

  const [provider, setProvider] = useState("github");
  const [name, setName] = useState("");
  const [token, setToken] = useState("");

  const add = useMutation({
    mutationFn: () =>
      api("/git/connections", {
        method: "POST",
        body: JSON.stringify({ provider, name, token }),
      }),
    onSuccess: () => {
      setName("");
      setToken("");
      qc.invalidateQueries({ queryKey: ["git", "connections"] });
    },
  });
  const remove = useMutation({
    mutationFn: (id: number) =>
      api(`/git/connections/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["git", "connections"] }),
  });

  return (
    <section>
      <h2 className="text-base font-medium">Git connections</h2>
      <p className="mt-1 text-sm text-zinc-500">
        Personal access tokens for private repositories. Stored encrypted;
        used only during git sync, never written to disk.
      </p>

      <form
        className="mt-4 flex items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          add.mutate();
        }}
      >
        <label>
          <span className="mb-1 block text-xs text-zinc-500">Provider</span>
          <select
            value={provider}
            onChange={(e) => setProvider(e.target.value)}
            className="rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          >
            <option value="github">GitHub</option>
            <option value="gitlab">GitLab</option>
          </select>
        </label>
        <label>
          <span className="mb-1 block text-xs text-zinc-500">Name</span>
          <input
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="acme-bot"
            className="w-36 rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          />
        </label>
        <label className="flex-1">
          <span className="mb-1 block text-xs text-zinc-500">Token</span>
          <input
            required
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="ghp_… / glpat-…"
            className="w-full rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
          />
        </label>
        <button
          type="submit"
          disabled={add.isPending}
          className="rounded-md bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
        >
          Add
        </button>
      </form>
      {add.isError && (
        <p className="mt-2 text-sm text-red-400">
          {add.error instanceof Error ? add.error.message : "Failed"}
        </p>
      )}

      <div className="mt-4 space-y-2">
        {connections.data?.map((c) => (
          <div
            key={c.id}
            className="flex items-center justify-between rounded-md border border-zinc-900 bg-zinc-900/40 px-4 py-2.5"
          >
            <div className="text-sm">
              {c.name}
              <span className="ml-2 text-xs text-zinc-500">{c.provider}</span>
            </div>
            <button
              onClick={() => remove.mutate(c.id)}
              className="text-xs text-zinc-500 hover:text-red-400"
            >
              Remove
            </button>
          </div>
        ))}
        {connections.data?.length === 0 && (
          <p className="text-sm text-zinc-600">No connections yet.</p>
        )}
      </div>
    </section>
  );
}
