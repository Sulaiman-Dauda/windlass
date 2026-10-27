import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";

export interface Project {
  id: number;
  name: string;
  source: "manual" | "git" | "template";
  git_repo?: string;
  git_branch?: string;
  auto_deploy: boolean;
  created_at: string;
  /** Set on a staging: the live project it belongs to. */
  staging_of?: string;
  /** Set on a live project that has a staging. */
  staging?: string;
}

export interface ProjectFile {
  name: string;
  size: number;
  is_dir: boolean;
  mod_time: string;
}

export function useProjects() {
  return useQuery<Project[]>({
    queryKey: ["projects"],
    queryFn: () => api<Project[]>("/projects"),
  });
}

export function useProject(name: string, enabled = true) {
  return useQuery<Project>({
    queryKey: ["projects", name],
    queryFn: () => api<Project>(`/projects/${name}`),
    enabled,
  });
}

export function useCreateProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string }) =>
      api<Project>("/projects", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["projects"] }),
  });
}

export function useScanProjects() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<{ count: number }>("/projects/scan", { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["projects"] }),
  });
}

export function useDeleteProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, password }: { name: string; password?: string }) =>
      api(`/projects/${name}`, {
        method: "DELETE",
        body: JSON.stringify(password ? { password } : {}),
      }),
    // Only the list: refetching the deleted project's own queries 404s them,
    // which swaps the page to "not found" and unmounts the dialog before its
    // onSuccess can navigate away.
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects"], exact: true });
      qc.invalidateQueries({ queryKey: ["staging"] });
    },
  });
}

export function useProjectFiles(name: string) {
  return useQuery<ProjectFile[]>({
    queryKey: ["projects", name, "files"],
    queryFn: () => api<ProjectFile[]>(`/projects/${name}/files`),
  });
}

export function useProjectFile(name: string, path: string | null) {
  return useQuery<{ path: string; content: string }>({
    queryKey: ["projects", name, "files", path],
    queryFn: () => api(`/projects/${name}/files/${path}`),
    enabled: path !== null,
  });
}

export function useSaveProjectFile(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path, content }: { path: string; content: string }) =>
      api(`/projects/${name}/files/${path}`, {
        method: "PUT",
        body: JSON.stringify({ content }),
      }),
    onSuccess: (_data, { path }) =>
      qc.invalidateQueries({ queryKey: ["projects", name, "files", path] }),
  });
}

export function useProjectEnv(name: string) {
  return useQuery<Record<string, string>>({
    queryKey: ["projects", name, "env"],
    queryFn: () => api(`/projects/${name}/env`),
  });
}

export function useSaveProjectEnv(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: Record<string, string>) =>
      api(`/projects/${name}/env`, {
        method: "PUT",
        body: JSON.stringify(vars),
      }),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["projects", name, "env"] }),
  });
}

/**
 * Puts each staging straight after its live project, keeping the given order
 * otherwise. A staging whose live is not in the list (filtered out, or gone)
 * stays where it was.
 */
export function withStagingNested(projects: Project[]): Project[] {
  const names = new Set(projects.map((p) => p.name));
  const byLive = new Map<string, Project[]>();
  for (const p of projects) {
    if (p.staging_of && names.has(p.staging_of)) byLive.set(p.staging_of, [...(byLive.get(p.staging_of) ?? []), p]);
  }
  return projects.flatMap((p) => (p.staging_of && names.has(p.staging_of) ? [] : [p, ...(byLive.get(p.name) ?? [])]));
}
