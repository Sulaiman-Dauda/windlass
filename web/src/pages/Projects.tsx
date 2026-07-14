import { useState } from "react";
import { useCreateProject, useProjects, useScanProjects } from "../api/projects";
import { Page, EmptyState } from "../ui/Page";
import { CardLink, Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { Input } from "../ui/Field";
import { Icon } from "../ui/Icon";

export default function Projects() {
  const projects = useProjects();
  const create = useCreateProject();
  const scan = useScanProjects();
  const [name, setName] = useState("");
  const [showCreate, setShowCreate] = useState(false);

  return (
    <Page
      title="Projects"
      subtitle={projects.data ? `${projects.data.length} project${projects.data.length === 1 ? "" : "s"}` : undefined}
      actions={
        <>
          <Button size="sm" onClick={() => scan.mutate()} disabled={scan.isPending}>
            <Icon name="refresh" size={15} />
            {scan.isPending ? "Scanning…" : "Scan directory"}
          </Button>
          <Button size="sm" variant="primary" onClick={() => setShowCreate((v) => !v)}>
            <Icon name="plus" size={15} /> New project
          </Button>
        </>
      }
    >
      {scan.isError && (
        <p className="mb-4 text-sm text-err">
          {scan.error instanceof Error ? scan.error.message : "Stack scan failed"}
        </p>
      )}

      {showCreate && (
        <Card className="mb-6 p-4">
          <form
            className="flex items-start gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              create.mutate({ name }, { onSuccess: () => { setName(""); setShowCreate(false); } });
            }}
          >
            <div className="flex-1">
              <Input
                autoFocus
                value={name}
                onChange={(e) => setName(e.target.value.toLowerCase())}
                placeholder="project-name (lowercase, digits, - and _)"
                pattern="[a-z0-9][a-z0-9_-]*"
                required
              />
              {create.isError && (
                <p className="mt-1.5 text-sm text-err">
                  {create.error instanceof Error ? create.error.message : "Failed"}
                </p>
              )}
            </div>
            <Button type="submit" variant="primary" disabled={create.isPending}>
              Create
            </Button>
          </form>
        </Card>
      )}

      {projects.data && projects.data.length === 0 && !showCreate ? (
        <EmptyState
          icon={<Icon name="projects" size={26} />}
          title="No projects yet"
          desc="Create one to get started, or scan the stacks directory to import existing Compose apps."
        />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {projects.data?.map((p) => (
            <CardLink key={p.name} to={`/projects/${p.name}`} className="p-4">
              <div className="flex items-center justify-between">
                <span className="text-md font-semibold tracking-[-0.01em]">{p.name}</span>
                <Icon name="chevronRight" size={16} className="text-fg3" />
              </div>
              <div className="mt-2 truncate font-mono text-xs text-fg3">
                {p.source}
                {p.git_repo ? ` · ${p.git_repo}` : ""}
              </div>
            </CardLink>
          ))}
        </div>
      )}
    </Page>
  );
}
