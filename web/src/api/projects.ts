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

export function useProject(name: string) {
  return useQuery<Project>({
    queryKey: ["projects", name],
    queryFn: () => api<Project>(`/projects/${name}`),
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
    onSuccess: () => qc.invalidateQueries({ queryKey: ["projects"] }),
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
