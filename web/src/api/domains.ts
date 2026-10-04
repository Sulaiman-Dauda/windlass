import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";

export interface Domain {
  hostname: string;
  service: string;
  container_port: number;
  status: "active" | "pending" | "proxy_unavailable";
}

export function useDomains(project: string, poll: number | false = 5000) {
  return useQuery<Domain[]>({
    queryKey: ["projects", project, "domains"],
    queryFn: () => api<Domain[]>(`/projects/${project}/domains`),
    refetchInterval: poll,
  });
}

export function useProxyStatus() {
  return useQuery<{ available: boolean; version: string }>({
    queryKey: ["proxy", "status"],
    queryFn: () => api("/proxy/status"),
  });
}

export function useAddDomain(project: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { hostname: string; service: string; container_port: number }) =>
      api(`/projects/${project}/domains`, { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["projects", project, "domains"] }),
  });
}

export function useRemoveDomain(project: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (hostname: string) => api(`/projects/${project}/domains/${hostname}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["projects", project, "domains"] }),
  });
}
