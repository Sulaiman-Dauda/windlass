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
  /** The Windlass process itself; rss_bytes is 0 where it cannot be read. */
  panel?: { rss_bytes: number };
}

/** The idle-RSS ceiling CI holds the panel to (CLAUDE.md rule 8). */
export const PANEL_RSS_BUDGET_BYTES = 40 * 1024 * 1024;

export interface AttentionItem {
  kind: "deploy_failed" | "disk" | "no_backup" | "proxy" | "update" | string;
  tone: "error" | "warn" | "info";
  project?: string;
  title: string;
  detail?: string;
  link?: string;
  action?: string;
}

export interface AttentionList {
  checked_at: string;
  items: AttentionItem[];
}

/** The Needs you list. The server caches it, so a 30 second poll is cheap. */
export function useAttention() {
  return useQuery<AttentionList>({
    queryKey: ["system", "attention"],
    queryFn: () => api<AttentionList>("/system/attention"),
    refetchInterval: 30_000,
    staleTime: 15_000,
    retry: false,
  });
}

interface DiskKind {
  count: number;
  active_count: number;
  bytes: number;
  reclaimable_bytes: number;
}

export interface SystemDisk {
  host: { used_bytes: number; total_bytes: number };
  /** Absent when Docker cannot be reached. */
  docker?: { images: DiskKind; containers: DiskKind; volumes: DiskKind; build_cache: DiskKind };
  checked_at?: string;
}

/** Host disk and Docker's share of it. Settings reads the same cache entry. */
export function useSystemDisk() {
  return useQuery<SystemDisk>({
    queryKey: ["system", "disk"],
    queryFn: () => api<SystemDisk>("/system/disk"),
    refetchInterval: 60_000,
    retry: false,
  });
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
  /** False when this install cannot update itself (container image, WINDLASS_NO_SELF_UPDATE). */
  apply_supported: boolean;
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
