import { useQuery } from "@tanstack/react-query";
import { api } from "./client";

export interface MCPTool {
  name: string;
  summary: string;
  needs: "read" | "deploy";
  destructive: boolean;
}

/** Every tool the MCP server has, in its order. Fixed while the server runs. */
export function useMCPTools() {
  return useQuery<MCPTool[]>({ queryKey: ["mcp", "tools"], queryFn: () => api("/mcp/tools"), staleTime: Infinity });
}

/** The HTTPS endpoint under the panel domain; url is empty when none is set. */
export function useMCPEndpoint() {
  return useQuery<{ url: string }>({ queryKey: ["mcp", "endpoint"], queryFn: () => api("/mcp/endpoint"), retry: false });
}
