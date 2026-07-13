import { useState } from "react";
import { Link } from "react-router-dom";
import { useCreateProject, useProjects } from "../api/projects";

export default function Projects() {
  const projects = useProjects();
  const create = useCreateProject();
  const [name, setName] = useState("");
  const [showCreate, setShowCreate] = useState(false);

  return (
    <div>
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Projects</h1>
        <button
          onClick={() => setShowCreate((v) => !v)}
          className="rounded-md bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white"
        >
          New project
        </button>
      </div>

      {showCreate && (
        <form
          className="mt-4 flex items-start gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate(
              { name },
              {
                onSuccess: () => {
                  setName("");
                  setShowCreate(false);
                },
              },
            );
          }}
        >
          <div className="flex-1">
            <input
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value.toLowerCase())}
              placeholder="project-name (lowercase, digits, - and _)"
              pattern="[a-z0-9][a-z0-9_-]*"
              required
              className="w-full max-w-sm rounded-md border border-zinc-800 bg-zinc-900 px-3 py-1.5 text-sm text-zinc-100 outline-none focus:border-zinc-600"
            />
            {create.isError && (
              <p className="mt-1 text-sm text-red-400">
                {create.error instanceof Error ? create.error.message : "Failed"}
              </p>
            )}
          </div>
          <button
            type="submit"
            disabled={create.isPending}
            className="rounded-md border border-zinc-700 px-3 py-1.5 text-sm hover:bg-zinc-900 disabled:opacity-50"
          >
            Create
          </button>
        </form>
      )}

      <div className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {projects.data?.map((p) => (
          <Link
            key={p.name}
            to={`/projects/${p.name}`}
            className="rounded-lg border border-zinc-900 bg-zinc-900/50 p-4 hover:border-zinc-700"
          >
            <div className="font-medium">{p.name}</div>
            <div className="mt-1 text-xs text-zinc-500">
              {p.source}
              {p.git_repo ? ` · ${p.git_repo}` : ""}
            </div>
          </Link>
        ))}
        {projects.data?.length === 0 && !showCreate && (
          <p className="text-sm text-zinc-500">
            No projects yet. Create one to get started.
          </p>
        )}
      </div>
    </div>
  );
}
