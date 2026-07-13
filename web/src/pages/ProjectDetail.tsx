import { useEffect, useState } from "react";
import {
  NavLink,
  Route,
  Routes,
  useNavigate,
  useParams,
} from "react-router-dom";
import {
  useDeleteProject,
  useProject,
  useProjectEnv,
  useProjectFile,
  useProjectFiles,
  useSaveProjectEnv,
  useSaveProjectFile,
} from "../api/projects";
import DeploymentsTab from "../components/DeploymentsTab";
import DomainsTab from "../components/DomainsTab";
import GitTab from "../components/GitTab";
import LogsTab from "../components/LogsTab";
import OverviewTab from "../components/OverviewTab";
import TerminalTab from "../components/TerminalTab";

export default function ProjectDetail() {
  const { name = "" } = useParams();
  const project = useProject(name);
  const del = useDeleteProject();
  const navigate = useNavigate();

  if (project.isLoading) {
    return <p className="text-sm text-zinc-500">Loading…</p>;
  }
  if (project.isError) {
    return <p className="text-sm text-red-400">Project not found.</p>;
  }

  const tabs = [
    { to: "", label: "Overview", end: true },
    { to: "deployments", label: "Deployments" },
    { to: "domains", label: "Domains" },
    { to: "git", label: "Git" },
    { to: "files", label: "Files" },
    { to: "env", label: "Environment" },
    { to: "logs", label: "Logs" },
    { to: "terminal", label: "Terminal" },
  ];

  return (
    <div>
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">{name}</h1>
        <button
          onClick={() => {
            if (confirm(`Delete project "${name}"? Its containers are stopped and its directory removed.`)) {
              del.mutate(name, { onSuccess: () => navigate("/projects") });
            }
          }}
          className="rounded-md border border-red-900/60 px-3 py-1.5 text-sm text-red-400 hover:bg-red-950/40"
        >
          Delete
        </button>
      </div>

      <nav className="mt-4 flex gap-1 border-b border-zinc-900">
        {tabs.map((t) => (
          <NavLink
            key={t.label}
            to={t.to}
            end={t.end}
            className={({ isActive }) =>
              `border-b-2 px-3 py-2 text-sm ${
                isActive
                  ? "border-zinc-100 text-zinc-100"
                  : "border-transparent text-zinc-500 hover:text-zinc-300"
              }`
            }
          >
            {t.label}
          </NavLink>
        ))}
      </nav>

      <div className="mt-6">
        <Routes>
          <Route index element={<OverviewTab project={name} />} />
          <Route path="deployments" element={<DeploymentsTab project={name} />} />
          <Route path="domains" element={<DomainsTab project={name} />} />
          <Route path="git" element={<GitTab project={name} />} />
          <Route path="files" element={<FilesTab name={name} />} />
          <Route path="env" element={<EnvTab name={name} />} />
          <Route path="logs" element={<LogsTab project={name} />} />
          <Route path="terminal" element={<TerminalTab project={name} />} />
        </Routes>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------

function FilesTab({ name }: { name: string }) {
  const files = useProjectFiles(name);
  const [selected, setSelected] = useState<string | null>("compose.yaml");
  const file = useProjectFile(name, selected);
  const save = useSaveProjectFile(name);
  const [draft, setDraft] = useState("");

  useEffect(() => {
    if (file.data) setDraft(file.data.content);
  }, [file.data]);

  return (
    <div className="flex gap-6">
      <div className="w-48 shrink-0">
        {files.data?.filter((f) => !f.IsDir).map((f) => (
          <button
            key={f.Name}
            onClick={() => setSelected(f.Name)}
            className={`block w-full truncate rounded px-2 py-1 text-left text-sm ${
              selected === f.Name
                ? "bg-zinc-800 text-zinc-100"
                : "text-zinc-400 hover:bg-zinc-900"
            }`}
          >
            {f.Name}
          </button>
        ))}
      </div>

      <div className="flex-1">
        {selected === null ? (
          <p className="text-sm text-zinc-500">Select a file.</p>
        ) : file.isError ? (
          <p className="text-sm text-red-400">
            {file.error instanceof Error ? file.error.message : "Cannot open file"}
          </p>
        ) : (
          <>
            <textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              spellCheck={false}
              rows={20}
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 p-3 font-mono text-sm text-zinc-100 outline-none focus:border-zinc-600"
            />
            <div className="mt-2 flex items-center gap-3">
              <button
                onClick={() => save.mutate({ path: selected, content: draft })}
                disabled={save.isPending || draft === file.data?.content}
                className="rounded-md bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
              >
                {save.isPending ? "Saving…" : "Save"}
              </button>
              {save.isError && (
                <span className="text-sm text-red-400">
                  {save.error instanceof Error ? save.error.message : "Save failed"}
                </span>
              )}
              {save.isSuccess && draft === file.data?.content && (
                <span className="text-sm text-emerald-400">Saved</span>
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------

interface EnvRow {
  key: string;
  value: string;
}

function EnvTab({ name }: { name: string }) {
  const env = useProjectEnv(name);
  const save = useSaveProjectEnv(name);
  const [rows, setRows] = useState<EnvRow[]>([]);
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (env.data && !dirty) {
      setRows(
        Object.entries(env.data)
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([key, value]) => ({ key, value })),
      );
    }
  }, [env.data, dirty]);

  const update = (i: number, patch: Partial<EnvRow>) => {
    setDirty(true);
    setRows((r) => r.map((row, j) => (j === i ? { ...row, ...patch } : row)));
  };

  return (
    <div className="max-w-2xl">
      <p className="text-sm text-zinc-500">
        Values are encrypted at rest and written to the project's{" "}
        <code className="text-zinc-400">.env</code> at deploy time.
      </p>

      <div className="mt-4 space-y-2">
        {rows.map((row, i) => (
          <div key={i} className="flex gap-2">
            <input
              value={row.key}
              onChange={(e) => update(i, { key: e.target.value.toUpperCase() })}
              placeholder="KEY"
              className="w-56 rounded-md border border-zinc-800 bg-zinc-900 px-2 py-1.5 font-mono text-sm text-zinc-100 outline-none focus:border-zinc-600"
            />
            <input
              value={row.value}
              onChange={(e) => update(i, { value: e.target.value })}
              placeholder="value"
              className="flex-1 rounded-md border border-zinc-800 bg-zinc-900 px-2 py-1.5 font-mono text-sm text-zinc-100 outline-none focus:border-zinc-600"
            />
            <button
              onClick={() => {
                setDirty(true);
                setRows((r) => r.filter((_, j) => j !== i));
              }}
              className="rounded-md px-2 text-zinc-500 hover:text-red-400"
              title="Remove"
            >
              ×
            </button>
          </div>
        ))}
      </div>

      <div className="mt-3 flex items-center gap-3">
        <button
          onClick={() => {
            setDirty(true);
            setRows((r) => [...r, { key: "", value: "" }]);
          }}
          className="rounded-md border border-zinc-700 px-3 py-1.5 text-sm hover:bg-zinc-900"
        >
          Add variable
        </button>
        <button
          onClick={() => {
            const vars: Record<string, string> = {};
            for (const r of rows) if (r.key) vars[r.key] = r.value;
            save.mutate(vars, { onSuccess: () => setDirty(false) });
          }}
          disabled={save.isPending || !dirty}
          className="rounded-md bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
        >
          {save.isPending ? "Saving…" : "Save changes"}
        </button>
        {save.isError && (
          <span className="text-sm text-red-400">
            {save.error instanceof Error ? save.error.message : "Save failed"}
          </span>
        )}
      </div>
    </div>
  );
}
