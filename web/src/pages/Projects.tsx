import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useCan } from "../api/auth";
import { useCreateProject, useProjects, useScanProjects } from "../api/projects";
import { Page, EmptyState, FormError } from "../ui/Page";
import { Button } from "../ui/Button";
import { Input, Field } from "../ui/Field";
import { Icon } from "../ui/Icon";
import { Modal } from "../ui/Modal";
import { useToast, errorText } from "../ui/Toast";
import { plural } from "../ui/format";
import ProjectsTable from "../components/ProjectsTable";

export default function Projects() {
  const projects = useProjects();
  const scan = useScanProjects();
  const can = useCan();
  const toast = useToast();
  const [params, setParams] = useSearchParams();
  const [creating, setCreating] = useState(false);
  const [filter, setFilter] = useState("");

  // /projects?new=1 (sidebar plus, command palette) opens the dialog directly.
  useEffect(() => {
    if (params.get("new") && can("member")) {
      setCreating(true);
      setParams({}, { replace: true });
    }
  }, [params, setParams, can]);

  const list = projects.data ?? [];
  const q = filter.trim().toLowerCase();
  const shown = q ? list.filter((p) => p.name.includes(q) || p.git_repo?.toLowerCase().includes(q)) : list;

  return (
    <Page
      title="Projects"
      description="Each project is a directory with a Compose file. The files on disk are the source of truth."
      actions={
        can("member") && (
          <>
            <Button
              icon="refresh"
              loading={scan.isPending}
              onClick={() =>
                scan.mutate(undefined, {
                  onSuccess: (r) => toast.ok("Scan complete", `${plural(r.count, "project")} found in the stacks directory.`),
                  onError: (e) => toast.err("Scan failed", errorText(e)),
                })
              }
            >
              Scan directory
            </Button>
            <Button variant="primary" icon="plus" onClick={() => setCreating(true)}>
              New project
            </Button>
          </>
        )
      }
    >
      {creating && <CreateProjectDialog onClose={() => setCreating(false)} />}

      {projects.data && list.length === 0 ? (
        <EmptyState
          icon="projects"
          title="No projects yet"
          desc="Create one to get started, or scan the stacks directory to adopt Compose apps that are already on this server."
          actions={
            can("member") && (
              <Button variant="primary" icon="plus" onClick={() => setCreating(true)}>
                Create your first project
              </Button>
            )
          }
        />
      ) : (
        <>
          <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
            <div className="relative w-full max-w-xs">
              <Icon name="search" size={15} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-fg3" />
              <Input
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder="Filter projects"
                aria-label="Filter projects"
                className="pl-8"
              />
            </div>
            {projects.data && (
              <span className="text-sm text-fg3">
                {q ? `${shown.length} of ${list.length}` : plural(list.length, "project")}
              </span>
            )}
          </div>
          {q && shown.length === 0 ? (
            <EmptyState icon="search" title="No matching projects" desc={`Nothing is called “${filter}”.`} />
          ) : (
            <ProjectsTable projects={shown} loading={projects.isLoading} />
          )}
        </>
      )}
    </Page>
  );
}

function CreateProjectDialog({ onClose }: { onClose: () => void }) {
  const create = useCreateProject();
  const navigate = useNavigate();
  const toast = useToast();
  const [name, setName] = useState("");

  return (
    <Modal
      onClose={onClose}
      title="New project"
      description="Windlass creates the directory with a starter compose.yaml you can edit before the first deploy."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="create-project" variant="primary" loading={create.isPending}>
            Create
          </Button>
        </>
      }
    >
      <form
        id="create-project"
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate(
            { name },
            {
              onSuccess: (p) => {
                toast.ok(`Created ${p.name}`, "Edit compose.yaml in Files, then deploy.");
                onClose();
                navigate(`/projects/${p.name}`);
              },
            },
          );
        }}
      >
        <Field label="Name" hint="Lowercase letters, digits, - and _. Becomes the directory and Compose project name.">
          <Input
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value.toLowerCase())}
            placeholder="project-name"
            pattern="[a-z0-9][a-z0-9_-]*"
            spellCheck={false}
            autoComplete="off"
            required
            className="font-mono"
          />
        </Field>
        <FormError error={create.error} fallback="Could not create the project" />
      </form>
    </Modal>
  );
}
