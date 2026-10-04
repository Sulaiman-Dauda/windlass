import { useEffect, useRef, useState } from "react";
import { useCan } from "../api/auth";
import { useProjectFile, useProjectFiles, useSaveProjectFile } from "../api/projects";
import { Button } from "../ui/Button";
import { Card, CardFooter } from "../ui/Card";
import { Tag, Kbd } from "../ui/Badge";
import { Callout } from "../ui/Page";
import { Icon } from "../ui/Icon";
import { Skeleton } from "../ui/Skeleton";
import { useToast, errorText } from "../ui/Toast";
import { cn } from "../ui/cn";
import { formatBytes } from "../ui/format";
import { isMac } from "./Layout";

export default function FilesTab({ project }: { project: string }) {
  const files = useProjectFiles(project);
  const [chosen, setSelected] = useState<string>("compose.yaml");
  const list = files.data?.filter((f) => !f.is_dir) ?? [];
  // Open compose.yaml by default, or the first file when there is none.
  const selected = list.some((f) => f.name === chosen) ? chosen : (list[0]?.name ?? null);

  return (
    <div className="grid grid-cols-1 gap-5 lg:grid-cols-[240px_minmax(0,1fr)]">
      <Card className="h-fit overflow-hidden">
        <div className="flex items-center justify-between border-b border-hairline px-4 py-3">
          <h3 className="text-sm font-semibold text-fg">Files</h3>
          {files.data && <span className="text-xs text-fg3">{list.length}</span>}
        </div>
        <ul className="max-h-[60vh] overflow-y-auto p-1.5">
          {files.isLoading && [0, 1, 2].map((i) => <Skeleton key={i} className="m-1.5 h-5" />)}
          {list.map((f) => (
            <li key={f.name}>
              <button
                type="button"
                onClick={() => setSelected(f.name)}
                aria-current={selected === f.name}
                className={cn(
                  "flex h-8 w-full items-center gap-2 rounded-control px-2.5 text-left transition-colors duration-100",
                  selected === f.name ? "bg-accent-soft text-accent" : "text-fg2 hover:bg-sunken hover:text-fg",
                )}
              >
                <Icon name="file" size={15} className={selected === f.name ? "" : "text-fg3"} />
                <span className="min-w-0 flex-1 truncate font-mono text-xs">{f.name}</span>
                <span className="text-2xs tabular-nums text-fg3">{formatBytes(f.size)}</span>
              </button>
            </li>
          ))}
          {files.data && list.length === 0 && <li className="px-2.5 py-2 text-xs text-fg3">The directory is empty.</li>}
        </ul>
      </Card>

      {selected && <Editor key={selected} project={project} path={selected} />}
    </div>
  );
}

function Editor({ project, path }: { project: string; path: string }) {
  const file = useProjectFile(project, path);
  const save = useSaveProjectFile(project);
  const can = useCan();
  const toast = useToast();
  const [draft, setDraft] = useState<string | null>(null);
  const editable = can("member");

  useEffect(() => {
    if (file.data) setDraft(file.data.content);
  }, [file.data]);

  const dirty = draft !== null && file.data !== undefined && draft !== file.data.content;
  const doSave = () => {
    if (!dirty || draft === null || save.isPending) return;
    save.mutate(
      { path, content: draft },
      {
        onSuccess: () => toast.ok(`Saved ${path}`, "Deploy to apply it to the running services."),
        onError: (e) => toast.err(`Could not save ${path}`, errorText(e)),
      },
    );
  };

  if (file.isError) {
    return (
      <Callout tone="err" title={`Can't open ${path}`}>
        {errorText(file.error, "The file could not be read.")}
      </Callout>
    );
  }

  return (
    <Card className="min-w-0 overflow-hidden">
      <div className="flex flex-wrap items-center gap-3 border-b border-hairline px-4 py-2.5">
        <Icon name="file" size={16} className="text-fg3" />
        <span className="font-mono text-sm font-medium text-fg">{path}</span>
        {dirty && <Tag tone="warn">Unsaved</Tag>}
        {!editable && <Tag>Read only</Tag>}
        {editable && (
          <div className="ml-auto flex items-center gap-2">
            {dirty && (
              <Button size="sm" variant="ghost" onClick={() => setDraft(file.data?.content ?? "")}>
                Revert
              </Button>
            )}
            <Button size="sm" variant="primary" onClick={doSave} disabled={!dirty} loading={save.isPending}>
              Save
              <span className="hidden gap-0.5 opacity-80 sm:inline-flex">
                <Kbd className="border-white/25 bg-white/10 text-onaccent shadow-none">{isMac ? "⌘" : "Ctrl"}</Kbd>
                <Kbd className="border-white/25 bg-white/10 text-onaccent shadow-none">S</Kbd>
              </span>
            </Button>
          </div>
        )}
      </div>
      {draft === null ? (
        <div className="space-y-2 p-4">
          <Skeleton className="h-4 w-2/3" />
          <Skeleton className="h-4 w-1/2" />
          <Skeleton className="h-4 w-3/5" />
        </div>
      ) : (
        <CodeEditor value={draft} onChange={setDraft} readOnly={!editable} onSave={doSave} label={path} />
      )}
      <CardFooter note="Saving writes the file on disk. Compose and .env changes take effect at the next deploy." />
    </Card>
  );
}

/** Plain textarea with a line-number gutter: no editor dependency to ship. */
function CodeEditor({
  value,
  onChange,
  readOnly,
  onSave,
  label,
}: {
  value: string;
  onChange: (v: string) => void;
  readOnly: boolean;
  onSave: () => void;
  label: string;
}) {
  const gutter = useRef<HTMLDivElement>(null);
  const lines = value.split("\n").length;

  return (
    <div className="flex h-[min(68vh,720px)] min-h-[360px] bg-surface font-mono text-[13px] leading-6">
      <div
        ref={gutter}
        aria-hidden="true"
        className="select-none overflow-hidden border-r border-hairline bg-surface2 py-3 pl-4 pr-3 text-right tabular-nums text-fg3"
      >
        {Array.from({ length: lines }, (_, i) => (
          <div key={i}>{i + 1}</div>
        ))}
      </div>
      <textarea
        aria-label={`Contents of ${label}`}
        value={value}
        readOnly={readOnly}
        onChange={(e) => onChange(e.target.value)}
        onScroll={(e) => {
          if (gutter.current) gutter.current.scrollTop = e.currentTarget.scrollTop;
        }}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
            e.preventDefault();
            onSave();
          }
        }}
        wrap="off"
        spellCheck={false}
        autoCapitalize="off"
        autoComplete="off"
        className="min-w-0 flex-1 resize-none bg-transparent px-4 py-3 text-fg outline-none focus-visible:outline-none"
      />
    </div>
  );
}
