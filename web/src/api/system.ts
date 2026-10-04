import { useQuery } from "@tanstack/react-query";
import { api } from "./client";

export interface Metrics {
  host: {
    cpu_percent: number;
    memory_used: number;
    memory_total: number;
    disk_used: number;
    disk_total: number;
    load1: number;
    uptime_seconds: number;
  };
  node: { hostname: string; docker_version: string; compose_version: string; caddy_version: string };
  containers: { running: number; total: number };
}

/** Host metrics, refreshed every `poll` milliseconds (or not at all). */
export function useMetrics(poll: number | false = false) {
  return useQuery<Metrics>({
    queryKey: ["system", "metrics"],
    queryFn: () => api<Metrics>("/system/metrics"),
    refetchInterval: poll,
    staleTime: 5_000,
  });
}

export interface UpdateInfo {
  version: string;
  current_version: string;
  update_available: boolean;
}

/** Release check (admin only). The sidebar and Settings share this cache entry. */
export function useUpdateCheck(enabled = true) {
  return useQuery<UpdateInfo>({
    queryKey: ["system", "update"],
    queryFn: () => api<UpdateInfo>("/system/update"),
    enabled,
    retry: false,
    staleTime: 60 * 60 * 1000,
    refetchInterval: 6 * 60 * 60 * 1000,
  });
}
