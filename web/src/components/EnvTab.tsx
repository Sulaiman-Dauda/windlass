import { useEffect, useState } from "react";
import { useCan } from "../api/auth";
import { useProjectEnv, useSaveProjectEnv } from "../api/projects";
import { Button, IconButton } from "../ui/Button";
import { Card, CardHeader, CardFooter } from "../ui/Card";
import { Input, Textarea } from "../ui/Field";
import { Segmented } from "../ui/Segmented";
import { Tag } from "../ui/Badge";
import { Skeleton } from "../ui/Skeleton";
import { useToast, errorText } from "../ui/Toast";
import { parseEnvBlock, serializeEnvBlock, type EnvRow } from "./envfile";

export default function EnvTab({ project }: { project: string }) {
  const env = useProjectEnv(project);
  const save = useSaveProjectEnv(project);
  const can = useCan();
  const toast = useToast();
  const editable = can("member");
  const [rows, setRows] = useState<EnvRow[]>([]);
  const [dirty, setDirty] = useState(false);
  // The text box is another view of the same rows, not a separate import
  // tool: opening it seeds the current rows, and applying it replaces rows
  // wholesale, including removing keys deleted from the pasted text, so
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

  const switchMode = (next: "list" | "bulk") => {
    if (next === mode) return;
    if (next === "bulk") {
      setBulkDraft(serializeEnvBlock(rows));
      setBulkErrors([]);
    }
    setMode(next);
  };

  const applyBulk = () => {
    const parsed = parseEnvBlock(bulkDraft);
    if (parsed.errors.length > 0) {
      setBulkErrors(parsed.errors);
      return;
    }
    setRows(parsed.rows.sort((a, b) => a.key.localeCompare(b.key)));
    setDirty(true);
    setBulkErrors([]);
    setMode("list");
  };

  const doSave = () => {
    const vars: Record<string, string> = {};
    for (const r of rows) if (r.key) vars[r.key] = r.value;
    save.mutate(vars, {
      onSuccess: () => {
        setDirty(false);
        toast.ok("Environment saved", "Deploy to give running containers the new values.");
      },
      onError: (e) => toast.err("Could not save the environment", errorText(e)),
    });
  };

  return (
    <Card>
      <CardHeader
        title="Environment variables"
        description={
          <>
            Stored in the project's <code className="font-mono text-fg2">.env</code> file (mode 0600), which stays the
            source of truth. Windlass keeps an encrypted copy for platform features.
          </>
        }
        actions={
          editable && (
            <Segmented
              size="sm"
              label="Editor view"
              value={mode}
              onChange={switchMode}
              options={[
                { value: "list", label: "Table" },
                { value: "bulk", label: ".env text" },
              ]}
            />
          )
        }
      />

      <div className="px-5 py-4">
        {env.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-9 w-full" />
          </div>
        ) : mode === "bulk" ? (
          <div>
            <p className="mb-2 text-xs text-fg3">
              One <code className="font-mono">KEY=value</code> per line. This always matches the table exactly: paste a
              whole <code className="font-mono">.env</code> file to replace everything, or delete a line to remove that
              variable.
            </p>
            <Textarea
              value={bulkDraft}
              onChange={(e) => {
                setBulkDraft(e.target.value);
                setBulkErrors([]);
              }}
              placeholder={"DATABASE_URL=postgres://app@db/app\nREDIS_HOST=redis\nPORT=3000"}
              spellCheck={false}
              rows={14}
              className="font-mono"
            />
            {bulkErrors.length > 0 && (
              <ul className="mt-2 space-y-1 text-xs text-err">
                {bulkErrors.map((error) => (
                  <li key={error}>{error}</li>
                ))}
              </ul>
            )}
            <div className="mt-3 flex gap-2">
              <Button variant="primary" size="sm" onClick={applyBulk}>
                Apply to table
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setBulkErrors([]);
                  setMode("list");
                }}
              >
                Cancel
              </Button>
            </div>
          </div>
        ) : rows.length === 0 ? (
          <div className="py-6 text-center text-sm text-fg3">
            No variables yet.
            {editable && " Add one, or paste a whole .env file in the text view."}
          </div>
        ) : (
          <div>
            <div className="mb-1.5 hidden grid-cols-[minmax(180px,280px)_minmax(0,1fr)_36px] gap-2 text-xs font-medium text-fg3 sm:grid">
              <span>Key</span>
              <span>Value</span>
            </div>
            <div className="space-y-2">
              {rows.map((row, i) => (
                <div key={i} className="grid grid-cols-[minmax(0,1fr)_36px] gap-2 sm:grid-cols-[minmax(180px,280px)_minmax(0,1fr)_36px]">
                  <Input
                    value={row.key}
                    readOnly={!editable}
                    onChange={(e) => update(i, { key: e.target.value.toUpperCase() })}
                    placeholder="KEY"
                    aria-label="Variable name"
                    spellCheck={false}
                    className="col-span-2 font-mono sm:col-span-1"
                  />
                  <Input
                    value={row.value}
                    readOnly={!editable}
                    onChange={(e) => update(i, { value: e.target.value })}
                    placeholder="value"
                    aria-label={`Value of ${row.key || "variable"}`}
                    spellCheck={false}
                    className="font-mono"
                  />
                  {editable && (
                    <IconButton
                      icon="trash"
                      label={`Remove ${row.key || "variable"}`}
                      size="md"
                      className="hover:bg-err-soft hover:text-err"
                      onClick={() => {
                        setDirty(true);
                        setRows((r) => r.filter((_, j) => j !== i));
                      }}
                    />
                  )}
                </div>
              ))}
            </div>
          </div>
        )}

        {editable && mode === "list" && !env.isLoading && (
          <Button
            size="sm"
            icon="plus"
            className="mt-3"
            onClick={() => {
              setDirty(true);
              setRows((r) => [...r, { key: "", value: "" }]);
            }}
          >
            Add variable
          </Button>
        )}
      </div>

      {editable && (
        <CardFooter
          note={
            dirty ? (
              <span className="inline-flex items-center gap-2">
                <Tag tone="warn">Unsaved</Tag> Changes apply to containers at the next deploy.
              </span>
            ) : (
              "Changes apply to containers at the next deploy."
            )
          }
        >
          {dirty && (
            <Button variant="ghost" size="sm" onClick={() => setDirty(false)} disabled={save.isPending}>
              Discard
            </Button>
          )}
          <Button variant="primary" size="sm" onClick={doSave} disabled={!dirty || mode === "bulk"} loading={save.isPending}>
            Save changes
          </Button>
        </CardFooter>
      )}
    </Card>
  );
}
