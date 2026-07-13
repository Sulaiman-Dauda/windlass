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

export function useCreateDeployment(project: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      api<Deployment>(`/projects/${project}/deployments`, { method: "POST" }),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["projects", project, "deployments"] }),
  });
}

export function useServices(project: string) {
  return useQuery<{ services: ServiceStatus[]; note?: string }>({
    queryKey: ["projects", project, "services"],
    queryFn: () => api(`/projects/${project}/services`),
    refetchInterval: 5000,
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
