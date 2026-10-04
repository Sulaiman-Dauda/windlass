import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";

export interface Deployment {
  id: number;
  number: number;
  status: string;
  triggered_by: string;
  git_commit?: string;
  error?: string;
  started_at?: string;
  finished_at?: string;
  created_at: string;
}

export interface ServiceStatus {
  service: string;
  name: string;
  state: string;
  health: string;
  exit_code: number;
  image: string;
  memory_limit?: number;
  cpu_limit?: number;
}

export const TERMINAL_STATUSES = ["succeeded", "failed", "cancelled"];

export function useDeployments(project: string) {
  return useQuery<Deployment[]>({
    queryKey: ["projects", project, "deployments"],
    queryFn: () => api(`/projects/${project}/deployments`),
    refetchInterval: (query) => {
      const active = query.state.data?.some(
        (d) => !TERMINAL_STATUSES.includes(d.status),
      );
      return active ? 1000 : false;
    },
  });
}

export function useRollback(project: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (number: number) =>
      api<Deployment>(`/projects/${project}/deployments/${number}/rollback`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["projects", project, "deployments"] }),
  });
}

export function useCreateDeployment(project: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      api<Deployment>(`/projects/${project}/deployments`, { method: "POST" }),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["projects", project, "deployments"] }),
  });
}

export function useServices(project: string, poll: number | false = 5000) {
  return useQuery<{ services: ServiceStatus[]; note?: string }>({
    queryKey: ["projects", project, "services"],
    queryFn: async () => {
      const result = await api<{
        services: ServiceStatus[] | null;
        note?: string;
      }>(`/projects/${project}/services`);
      return { ...result, services: result.services ?? [] };
    },
    refetchInterval: poll,
  });
}

export function useProjectAction(project: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (action: "start" | "stop" | "restart") =>
      api(`/projects/${project}/actions/${action}`, { method: "POST" }),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["projects", project, "services"] }),
  });
}

export interface DeployEvent {
  seq: number;
  type: "step" | "log" | "error" | "done";
  message: string;
  ts: string;
}

// useDeploymentEvents streams a deployment's event log over SSE into a
// bounded buffer. Logs are not cache-shaped data, so this bypasses
// TanStack Query entirely.
export function useDeploymentEvents(project: string, number: number | null) {
  const [eventLog, setEventLog] = useState<DeployEvent[]>([]);
  const [finished, setFinished] = useState(false);
  const sourceRef = useRef<EventSource | null>(null);

  useEffect(() => {
    setEventLog([]);
    setFinished(false);
    if (number === null) return;

    const es = new EventSource(
      `/api/v1/projects/${project}/deployments/${number}/events`,
    );
    sourceRef.current = es;

    const push = (raw: MessageEvent) => {
      try {
        const ev = JSON.parse(raw.data) as DeployEvent;
        setEventLog((log) => {
          const next = [...log, ev];
          return next.length > 2000 ? next.slice(-2000) : next;
        });
        if (ev.type === "done") {
          setFinished(true);
          es.close();
        }
      } catch {
        // ignore malformed frames
      }
    };

    for (const type of ["deployment.step", "deployment.log", "deployment.error", "deployment.done"]) {
      es.addEventListener(type, push);
    }
    es.onerror = () => {
      // EventSource auto-reconnects with Last-Event-ID; nothing to do
      // unless we already finished.
    };

    return () => {
      es.close();
      sourceRef.current = null;
    };
  }, [project, number]);

  return { eventLog, finished };
}

// ---------------------------------------------------------------------------
// Presentation helpers shared by the project list, header and deployments.

export type Tone = "ok" | "warn" | "err" | "idle" | "accent";

export interface ServicesSummary {
  tone: Tone;
  label: string;
  running: number;
  total: number;
}

/** One status for a whole project, from its compose ps rows. */
export function summariseServices(services: ServiceStatus[] | undefined): ServicesSummary | null {
  if (!services) return null;
  const total = services.length;
  const running = services.filter((s) => s.state === "running").length;
  const unhealthy = services.some((s) => s.health === "unhealthy" || s.state === "restarting" || s.state === "dead");
  if (total === 0) return { tone: "idle", label: "Not running", running, total };
  if (unhealthy) return { tone: "err", label: "Unhealthy", running, total };
  if (running === total) return { tone: "ok", label: "Running", running, total };
  if (running === 0) return { tone: "idle", label: "Stopped", running, total };
  return { tone: "warn", label: "Partial", running, total };
}

export function deploymentTone(status: string): Tone {
  if (status === "succeeded") return "ok";
  if (status === "failed") return "err";
  if (status === "cancelled") return "idle";
  return "accent";
}

export function isActive(status: string): boolean {
  return !TERMINAL_STATUSES.includes(status);
}

/** The pipeline's stages, in order, keyed by the status a running deployment reports. */
export const PIPELINE = [
  { key: "preparing", label: "Prepare" },
  { key: "syncing", label: "Sync" },
  { key: "pulling", label: "Pull" },
  { key: "building", label: "Build" },
  { key: "applying", label: "Start" },
  { key: "verifying", label: "Verify" },
] as const;

export type StageState = "pending" | "active" | "done" | "skipped" | "failed";

// Step events carry prose, not stage keys. These prefixes are the messages
// internal/deploy/pipeline.go emits; an unknown message simply does not move
// the stepper.
const stagePrefixes: [string, string][] = [
  ["rendering environment", "preparing"],
  ["pinning images", "preparing"],
  ["syncing ", "syncing"],
  ["pulling images", "pulling"],
  ["building images", "building"],
  ["starting services", "applying"],
  ["waiting for services", "verifying"],
  ["all services", "verifying"],
];

function stageOf(message: string): string | null {
  for (const [prefix, key] of stagePrefixes) if (message.startsWith(prefix)) return key;
  return null;
}

/** Where each stage stands, from the events seen so far and the reported status. */
export function stageStates(events: DeployEvent[], status: string): Record<string, StageState> {
  const keys = PIPELINE.map((p) => p.key as string);
  const seen = new Set<string>();
  for (const ev of events) {
    if (ev.type !== "step") continue;
    const k = stageOf(ev.message);
    if (k) seen.add(k);
  }
  const lastSeen = Math.max(-1, ...Array.from(seen, (k) => keys.indexOf(k)));
  const out: Record<string, StageState> = {};

  if (isActive(status)) {
    const current = keys.indexOf(status);
    const at = Math.max(current, lastSeen);
    keys.forEach((k, i) => {
      if (status === "queued" || at < 0) out[k] = "pending";
      else if (i < at) out[k] = seen.has(k) ? "done" : "skipped";
      else if (i === at) out[k] = "active";
      else out[k] = "pending";
    });
    return out;
  }

  keys.forEach((k, i) => {
    if (i === lastSeen && status !== "succeeded") out[k] = "failed";
    else if (seen.has(k)) out[k] = "done";
    else if (i < lastSeen || status === "succeeded") out[k] = "skipped";
    else out[k] = "pending";
  });
  return out;
}
