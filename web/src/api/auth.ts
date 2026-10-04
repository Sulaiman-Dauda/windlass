import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";

export interface User {
  id: number;
  email: string;
  role: "admin" | "member" | "viewer";
}

export interface AuthStatus {
  needs_setup: boolean;
  authenticated: boolean;
  user?: User;
}

export function useAuthStatus() {
  return useQuery<AuthStatus>({
    queryKey: ["auth", "status"],
    queryFn: () => api<AuthStatus>("/auth/status"),
    staleTime: 30_000,
  });
}

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; password: string; totp_code?: string }) =>
      api("/auth/login", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["auth"] }),
  });
}

export function useSetup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { token: string; email: string; password: string }) =>
      api("/auth/setup", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["auth"] }),
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api("/auth/logout", { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["auth"] }),
  });
}

export type Role = User["role"];

const rank: Record<Role, number> = { viewer: 0, member: 1, admin: 2 };

export function useCurrentUser(): User | undefined {
  return useAuthStatus().data?.user;
}

/**
 * Whether the signed-in user holds at least `role`. This only decides what is
 * rendered: every handler enforces the same rule on the server.
 */
export function useCan(): (role: Role) => boolean {
  const user = useCurrentUser();
  return (role) => Boolean(user && rank[user.role] >= rank[role]);
}

export interface Me {
  id: number;
  email: string;
  role: Role;
  totp_enabled: boolean;
  has_password: boolean;
}

export function useMe() {
  return useQuery<Me>({ queryKey: ["auth", "me"], queryFn: () => api<Me>("/auth/me") });
}
