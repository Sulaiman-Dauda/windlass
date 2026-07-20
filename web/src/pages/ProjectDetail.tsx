import { useEffect, useState, lazy, Suspense } from "react";
import { NavLink, Route, Routes, useNavigate, useParams } from "react-router-dom";
import {
  useProject,
  useProjectEnv,
  useProjectFile,
  useProjectFiles,
  useSaveProjectEnv,
  useSaveProjectFile,
} from "../api/projects";
import BackupsTab from "../components/BackupsTab";
import DeploymentsTab from "../components/DeploymentsTab";
import DomainsTab from "../components/DomainsTab";
import GitTab from "../components/GitTab";
import LogsTab from "../components/LogsTab";
import OverviewTab from "../components/OverviewTab";
import DeleteProjectDialog from "../components/DeleteProjectDialog";
import { Page } from "../ui/Page";
import { Button } from "../ui/Button";
import { Input, Textarea } from "../ui/Field";
import { Icon } from "../ui/Icon";
import { cn } from "../ui/cn";

const TerminalTab = lazy(() => import("../components/TerminalTab"));

const tabs = [
  { to: "", label: "Overview", end: true },
  { to: "deployments", label: "Deployments" },
  { to: "domains", label: "Domains" },
  { to: "git", label: "Git" },
  { to: "files", label: "Files" },
  { to: "env", label: "Environment" },
  { to: "logs", label: "Logs" },
  { to: "terminal", label: "Terminal" },
  { to: "backups", label: "Backups" },
];

export default function ProjectDetail() {
  const { name = "" } = useParams();
  const project = useProject(name);
  const navigate = useNavigate();
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  if (project.isLoading) {
    return <p className="p-10 text-sm text-fg3">Loading…</p>;
  }
  if (project.isError) {
    return <p className="p-10 text-sm text-err">Project not found.</p>;
  }

  return (
    <Page
      title={name}
      subtitle={project.data ? `${project.data.source}${project.data.git_repo ? ` · ${project.data.git_repo}` : ""}` : undefined}
      actions={
        <Button size="sm" variant="danger" onClick={() => setConfirmingDelete(true)}>
          <Icon name="trash" size={15} /> Delete
        </Button>
      }
    >
      {confirmingDelete && (
        <DeleteProjectDialog
          name={name}
          onClose={() => setConfirmingDelete(false)}
          onDeleted={() => navigate("/projects")}
        />
      )}

      <nav className="mb-6 flex gap-1 overflow-x-auto border-b border-hairline [scrollbar-width:none]">
        {tabs.map((t) => (
          <NavLink
            key={t.label}
            to={t.to}
            end={t.end}
            className={({ isActive }) =>
              cn(
                "relative whitespace-nowrap px-3 pb-3 pt-2.5 text-sm transition-colors duration-200",
                isActive
                  ? "font-semibold text-fg after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-t after:bg-accent"
                  : "font-medium text-fg2 hover:text-fg",
              )
            }
          >
            {t.label}
          </NavLink>
        ))}
      </nav>

      <Routes>
        <Route index element={<OverviewTab project={name} />} />
        <Route path="deployments" element={<DeploymentsTab project={name} />} />
        <Route path="domains" element={<DomainsTab project={name} />} />
        <Route path="git" element={<GitTab project={name} />} />
        <Route path="files" element={<FilesTab name={name} />} />
        <Route path="env" element={<EnvTab name={name} />} />
        <Route path="logs" element={<LogsTab project={name} />} />
        <Route
          path="terminal"
          element={
            <Suspense fallback={<p className="text-sm text-fg3">Loading terminal…</p>}>
              <TerminalTab project={name} />
            </Suspense>
          }
        />
        <Route path="backups" element={<BackupsTab project={name} />} />
      </Routes>
    </Page>
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
      <div className="w-52 flex-none">
        {files.data?.filter((f) => !f.is_dir).map((f) => (
          <button
            key={f.name}
            onClick={() => setSelected(f.name)}
            className={cn(
              "block w-full truncate rounded-[8px] px-2.5 py-1.5 text-left font-mono text-sm transition-colors duration-150",
              selected === f.name ? "bg-accent-soft text-accent" : "text-fg2 hover:bg-surface2 hover:text-fg",
            )}
          >
            {f.name}
          </button>
        ))}
      </div>

      <div className="min-w-0 flex-1">
        {selected === null ? (
          <p className="text-sm text-fg3">Select a file.</p>
        ) : file.isError ? (
          <p className="text-sm text-err">
            {file.error instanceof Error ? file.error.message : "Cannot open file"}
          </p>
        ) : (
          <>
            <Textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              spellCheck={false}
              rows={22}
              className="font-mono text-sm"
            />
            <div className="mt-2.5 flex items-center gap-3">
              <Button
                variant="primary"
                onClick={() => save.mutate({ path: selected, content: draft })}
                disabled={save.isPending || draft === file.data?.content}
              >
                {save.isPending ? "Saving…" : "Save"}
              </Button>
              {save.isError && (
                <span className="text-sm text-err">
                  {save.error instanceof Error ? save.error.message : "Save failed"}
                </span>
              )}
              {save.isSuccess && draft === file.data?.content && (
                <span className="text-sm text-ok">Saved</span>
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

function parseEnvBlock(input: string): { rows: EnvRow[]; errors: string[] } {
  const values = new Map<string, string>();
  const errors: string[] = [];

  input.split(/\r?\n/).forEach((source, index) => {
    let line = source.trim();
    if (!line || line.startsWith("#")) return;
    if (line.startsWith("export ")) line = line.slice(7).trimStart();

    const separator = line.indexOf("=");
    if (separator < 1) {
      errors.push(`Line ${index + 1}: expected KEY=value`);
      return;
    }

    const key = line.slice(0, separator).trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
      errors.push(`Line ${index + 1}: invalid variable name "${key}"`);
      return;
    }

    let value = line.slice(separator + 1).trim();
    if (
      value.length >= 2 &&
      ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'")))
    ) {
      const quote = value[0];
      value = value.slice(1, -1);
      if (quote === '"') {
        value = value
          .replace(/\\n/g, "\n")
          .replace(/\\r/g, "\r")
          .replace(/\\t/g, "\t")
          .replace(/\\"/g, '"')
          .replace(/\\\\/g, "\\");
      }
    }

    values.set(key, value);
  });

  return { rows: Array.from(values, ([key, value]) => ({ key, value })), errors };
}

// serializeEnvValue is the inverse of parseEnvBlock's value parsing: quote
// only when needed for the round-trip to be lossless (leading/trailing
// whitespace, or a value that would otherwise look quoted itself).
function serializeEnvValue(value: string): string {
  if (value === value.trim() && !/^['"]/.test(value)) return value;
  const escaped = value
    .replace(/\\/g, "\\\\")
    .replace(/"/g, '\\"')
    .replace(/\n/g, "\\n")
    .replace(/\r/g, "\\r")
    .replace(/\t/g, "\\t");
  return `"${escaped}"`;
}

function serializeEnvBlock(rows: EnvRow[]): string {
  return rows
    .filter((row) => row.key)
    .map((row) => `${row.key}=${serializeEnvValue(row.value)}`)
    .join("\n");
}

function EnvTab({ name }: { name: string }) {
  const env = useProjectEnv(name);
  const save = useSaveProjectEnv(name);
  const [rows, setRows] = useState<EnvRow[]>([]);
  const [dirty, setDirty] = useState(false);
  // The bulk box is another view of the same rows, not a separate import
  // tool: opening it seeds the current rows, and applying it replaces rows
  // wholesale — including removing keys deleted from the pasted text — so
  // the two views can never drift apart.
  const [mode, setMode] = useState<"list" | "bulk">("list");
  const [bulkDraft, setBulkDraft] = useState("");
  const [bulkErrors, setBulkErrors] = useState<string[]>([]);

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

  const openBulk = () => {
    setBulkDraft(serializeEnvBlock(rows));
    setBulkErrors([]);
    setMode("bulk");
  };

  const applyBulk = () => {
    const parsed = parseEnvBlock(bulkDraft);
    if (parsed.errors.length > 0) { setBulkErrors(parsed.errors); return; }
    setRows(parsed.rows.sort((a, b) => a.key.localeCompare(b.key)));
    setDirty(true);
    setBulkErrors([]);
    setMode("list");
  };

  return (
    <div className="w-full">
      <p className="text-sm leading-relaxed text-fg2">
        The project's standard <code className="font-mono text-fg">.env</code> file is the source of
        truth (server mode 0600). Windlass also keeps an encrypted SQLite cache for platform features.
      </p>

      <div className="mt-4">
        <Button size="sm" onClick={() => (mode === "bulk" ? setMode("list") : openBulk())}>
          {mode === "bulk" ? "Back to list view" : "Edit as .env text"}
        </Button>

        {mode === "bulk" && (
          <div className="mt-3 rounded-[13px] border border-hairline bg-surface2 p-3.5">
            <p className="mb-2 text-xs text-fg3">
              One <code className="font-mono">KEY=value</code> per line — this always matches the list
              below exactly, so paste a whole <code className="font-mono">.env</code> file to replace
              everything, or delete a line to remove that variable.
            </p>
            <Textarea
              value={bulkDraft}
              onChange={(e) => { setBulkDraft(e.target.value); setBulkErrors([]); }}
              placeholder={"AP_ENVIRONMENT=prod\nAP_REDIS_HOST=redis\nAP_REDIS_PORT=6379"}
              spellCheck={false}
              rows={12}
              className="font-mono text-sm"
            />
            {bulkErrors.length > 0 && (
              <ul className="mt-2 space-y-1 text-xs text-err">
                {bulkErrors.map((error) => <li key={error}>{error}</li>)}
              </ul>
            )}
            <div className="mt-2.5 flex gap-2">
              <Button variant="primary" onClick={applyBulk}>Apply to list</Button>
              <Button variant="ghost" onClick={() => { setBulkErrors([]); setMode("list"); }}>Cancel</Button>
            </div>
          </div>
        )}
      </div>

      {mode === "list" && (
        <div className="mt-4 space-y-2">
          {rows.map((row, i) => (
            <div key={i} className="flex gap-2">
              {/* Width lives on these wrapper divs, not on Input's own
                  className: Input's base style already includes w-full, and
                  cn() does no Tailwind conflict resolution, so a width class
                  passed directly to Input can lose to that w-full depending
                  on generated CSS order. A wrapper with no competing width
                  class sidesteps the ambiguity entirely. */}
              <div className="w-56 flex-none">
                <Input
                  value={row.key}
                  onChange={(e) => update(i, { key: e.target.value.toUpperCase() })}
                  placeholder="KEY"
                  className="font-mono text-sm"
                />
              </div>
              <div className="min-w-0 flex-1">
                <Input
                  value={row.value}
                  onChange={(e) => update(i, { value: e.target.value })}
                  placeholder="value"
                  className="font-mono text-sm"
                />
              </div>
              <button
                onClick={() => { setDirty(true); setRows((r) => r.filter((_, j) => j !== i)); }}
                className="grid w-9 flex-none place-items-center rounded-[9px] text-fg3 transition-colors hover:bg-err-soft hover:text-err"
                title="Remove"
              >
                <Icon name="x" size={15} />
              </button>
            </div>
          ))}
        </div>
      )}

      <div className="mt-3 flex items-center gap-3">
        {mode === "list" && (
          <Button size="sm" onClick={() => { setDirty(true); setRows((r) => [...r, { key: "", value: "" }]); }}>
            <Icon name="plus" size={15} /> Add variable
          </Button>
        )}
        <Button
          variant="primary"
          onClick={() => {
            const vars: Record<string, string> = {};
            for (const r of rows) if (r.key) vars[r.key] = r.value;
            save.mutate(vars, { onSuccess: () => setDirty(false) });
          }}
          disabled={save.isPending || !dirty || mode === "bulk"}
        >
          {save.isPending ? "Saving…" : "Save changes"}
        </Button>
        {save.isError && (
          <span className="text-sm text-err">
            {save.error instanceof Error ? save.error.message : "Save failed"}
          </span>
        )}
      </div>
    </div>
  );
}
