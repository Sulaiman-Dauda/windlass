import { useQuery } from "@tanstack/react-query";
import { api } from "./client";

export interface APIToken {
  id: number;
  name: string;
  prefix: string;
  scope: "read" | "deploy" | "write" | "admin";
  can_read_secrets: boolean;
  expires_at: string;
  expired: boolean;
  last_used_at: string | null;
  last_used_ip: string | null;
  created_at: string;
  revoked_at: string | null;
}

export type CreatedToken = APIToken & { token: string };

export const SCOPE_LABEL: Record<APIToken["scope"], string> = {
  read: "Read only",
  deploy: "Deploy only",
  write: "Deploy and edit",
  admin: "Admin",
};

/** The signed-in user's own tokens, newest first. */
export function useTokens() {
  return useQuery<APIToken[]>({ queryKey: ["tokens"], queryFn: () => api("/auth/tokens") });
}
