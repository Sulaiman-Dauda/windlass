import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "./client";
import type { Deployment } from "./deployments";
import type { Project } from "./projects";

export interface Issue {
  service?: string;
  message: string;
  fix?: string;
}

export interface DomainMap {
  live: string;
  staging: string;
}

export interface HostnameRewrite {
  from: string;
  to: string;
}

export interface DataItem {
  kind: "files" | "volume" | "database";
  name: string;
  service?: string;
  bytes: number;
  known: boolean;
}

export interface StagingRequest {
  branch?: string;
  domains?: DomainMap[];
  /** Only the values the person changed; the rest keep live's. */
  env?: Record<string, string>;
  copy_data?: boolean;
  auto_promote?: boolean;
  acknowledged?: boolean;
}

export interface Preflight {
  live: string;
  staging: string;
  branch?: string;
  git: boolean;
  blockers: Issue[];
  warnings: Issue[];
  flagged_env: { key: string; value: string }[];
  domains: DomainMap[];
  rewrites: HostnameRewrite[];
  data: DataItem[];
  projects_free_bytes: number | null;
  docker_free_bytes: number | null;
  can_create: boolean;
}

export interface CreateResult {
  project: Project;
  result: {
    deployment: number;
    rewritten: Record<string, number>;
    skipped: string[];
    webhook_secret?: string;
    webhook_registered: boolean;
  };
}

export interface DeploymentRef {
  number: number;
  status: string;
  commit?: string;
}

export interface StagingStatus {
  live: string;
  staging: string;
  auto_promote: boolean;
  branch?: string;
  live_branch?: string;
  live_current: DeploymentRef | null;
  live_latest: DeploymentRef | null;
  staging_latest: DeploymentRef | null;
  promotable: DeploymentRef | null;
  /** Why Promote is unavailable; absent when it is available. */
  promote_blocked?: string;
  rewrites: HostnameRewrite[];
  drift: { file: string; state: "changed" | "staging_only" | "live_only" }[];
}

export interface FileDiff {
  file: string;
  live: string;
  for_live: string;
}

const ACTIVE = (s?: string) => !!s && !["succeeded", "failed", "cancelled"].includes(s);

/**
 * The live and staging pair for either project's name, or null when there is
 * no staging. Changes invalidate every ["staging"] entry, so the two pages
 * refresh together.
 */
export function useStagingStatus(name: string, enabled = true) {
  return useQuery<StagingStatus | null>({
    queryKey: ["staging", name],
    queryFn: async () => {
      try {
        return await api<StagingStatus>(`/projects/${name}/staging`);
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
    enabled,
    refetchInterval: (q) => {
      const s = q.state.data;
      return s && (ACTIVE(s.staging_latest?.status) || ACTIVE(s.live_latest?.status)) ? 2000 : 15_000;
    },
  });
}

export function useStagingPreflight(live: string, req: StagingRequest, enabled: boolean) {
  return useQuery<Preflight>({
    queryKey: ["staging", live, "preflight", req],
    queryFn: () => api(`/projects/${live}/staging/preflight`, { method: "POST", body: JSON.stringify(req) }),
    enabled,
    placeholderData: keepPreviousData,
    staleTime: 10_000,
  });
}

export function useCreateStaging(live: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: StagingRequest) =>
      api<CreateResult>(`/projects/${live}/staging`, { method: "POST", body: JSON.stringify(req) }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      qc.invalidateQueries({ queryKey: ["staging"] });
    },
  });
}

export function useSetAutoPromote(staging: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (auto_promote: boolean) =>
      api(`/projects/${staging}/staging`, { method: "PUT", body: JSON.stringify({ auto_promote }) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["staging"] }),
  });
}

export function usePromote(staging: string, live: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (number: number) =>
      api<Deployment>(`/projects/${staging}/deployments/${number}/promote`, { method: "POST" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects", live, "deployments"] });
      qc.invalidateQueries({ queryKey: ["staging"] });
    },
  });
}

export function useStagingDiff(name: string, file: string | null) {
  return useQuery<FileDiff>({
    queryKey: ["staging", name, "diff", file],
    queryFn: () => api(`/projects/${name}/staging/diff/${file}`),
    enabled: file !== null,
  });
}

/** Writes staging's version of a compose file to live through the audited file PUT. */
export function useCopyToLive(live: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ file, content }: { file: string; content: string }) =>
      api(`/projects/${live}/files/${file}`, { method: "PUT", body: JSON.stringify({ content }) }),
    onSuccess: (_d, { file }) => {
      qc.invalidateQueries({ queryKey: ["projects", live, "files", file] });
      qc.invalidateQueries({ queryKey: ["staging"] });
    },
  });
}

export function shortCommit(c?: string) {
  return c ? c.slice(0, 7) : "";
}
