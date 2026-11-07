// Connection instructions for the MCP clients on the AI agents page. Every
// snippet and install link reads the token from the client's environment or
// asks for it: none may ever contain one. agentClients.test.ts holds that.

export type ClientId = "claude-code" | "cursor" | "vscode" | "codex" | "gemini" | "zed";

export const CLIENTS: { id: ClientId; label: string; tokenName: string }[] = [
  { id: "claude-code", label: "Claude Code", tokenName: "claude-code" },
  { id: "cursor", label: "Cursor", tokenName: "cursor" },
  { id: "vscode", label: "VS Code", tokenName: "vscode" },
  { id: "codex", label: "Codex", tokenName: "codex" },
  { id: "gemini", label: "Gemini CLI", tokenName: "gemini" },
  { id: "zed", label: "Zed", tokenName: "zed" },
];

export const SERVER_NAME = "windlass";
export const TOKEN_ENV = "WINDLASS_TOKEN";

export interface Snippet {
  title: string;
  path?: string;
  code: string;
}

export interface ClientSetup {
  install?: { label: string; href: string; note: string };
  snippets: Snippet[];
  note?: string;
}

export const CHECK_PROMPT = "Which projects are on my Windlass server?";

/** The MCP endpoint under a panel origin, which may carry a trailing slash. */
export function mcpEndpoint(origin: string): string {
  return `${origin.replace(/\/+$/, "")}/api/v1/mcp`;
}

const json = (v: unknown) => JSON.stringify(v, null, 2);

const exportLine = `export ${TOKEN_ENV}=...`;

/** Base64 of UTF-8 text, as btoa takes only Latin-1. */
function base64(text: string): string {
  let bin = "";
  for (const b of new TextEncoder().encode(text)) bin += String.fromCharCode(b);
  return btoa(bin);
}

/** VS Code's own server entry: it prompts for the token and keeps it in its secret storage. */
function vscodeServer(url: string) {
  return {
    type: "http",
    url,
    headers: { Authorization: "Bearer ${input:windlass-token}" },
  };
}

const vscodeInput = { type: "promptString", id: "windlass-token", description: "Windlass API token", password: true };

export function vscodeInstallLink(url: string): string {
  const payload = { name: SERVER_NAME, ...vscodeServer(url), inputs: [vscodeInput] };
  const link = `vscode:mcp/install?${encodeURIComponent(JSON.stringify(payload))}`;
  return `https://vscode.dev/redirect?url=${encodeURIComponent(link)}`;
}

function cursorServer(url: string) {
  return { url, headers: { Authorization: `Bearer \${env:${TOKEN_ENV}}` } };
}

export function cursorInstallLink(url: string): string {
  const config = base64(JSON.stringify(cursorServer(url)));
  return `cursor://anysphere.cursor-deeplink/mcp/install?name=${SERVER_NAME}&config=${encodeURIComponent(config)}`;
}

/**
 * The shell line Zed runs. mcp-remote takes --header text as it is and Zed
 * starts it with no shell, so sh is what fills the token in.
 */
export function zedBridge(url: string): string {
  return `exec npx -y mcp-remote ${url} --header "Authorization:Bearer $${TOKEN_ENV}"`;
}

export function clientSetup(id: ClientId, url: string): ClientSetup {
  switch (id) {
    case "claude-code":
      return {
        snippets: [
          { title: "Put the token in your environment, from your secret store rather than typed into shell history:", code: exportLine },
          {
            title: "Add the server to a project in",
            path: ".mcp.json",
            code: json({
              mcpServers: {
                [SERVER_NAME]: { type: "http", url, headers: { Authorization: `Bearer \${${TOKEN_ENV}}` } },
              },
            }),
          },
        ],
        note: `Claude Code fills \${${TOKEN_ENV}} from the environment it starts in. claude mcp add with --header would write the token itself into its configuration, so use the file.`,
      };
    case "cursor":
      return {
        install: { label: "Add to Cursor", href: cursorInstallLink(url), note: `Reads ${TOKEN_ENV} from the environment Cursor starts in.` },
        snippets: [
          { title: "Put the token in your environment, from your secret store rather than typed into shell history:", code: exportLine },
          { title: "Or add it by hand in", path: "~/.cursor/mcp.json", code: json({ mcpServers: { [SERVER_NAME]: cursorServer(url) } }) },
        ],
      };
    case "vscode":
      return {
        install: { label: "Add to VS Code", href: vscodeInstallLink(url), note: "VS Code asks for the token when the server first starts." },
        snippets: [
          {
            title: "Or add it by hand in",
            path: ".vscode/mcp.json",
            code: json({ inputs: [vscodeInput], servers: { [SERVER_NAME]: vscodeServer(url) } }),
          },
        ],
      };
    case "codex":
      return {
        snippets: [
          { title: "Put the token in your environment, from your secret store rather than typed into shell history:", code: exportLine },
          {
            title: "Add the server in",
            path: "~/.codex/config.toml",
            code: `[mcp_servers.${SERVER_NAME}]\nurl = "${url}"\nbearer_token_env_var = "${TOKEN_ENV}"`,
          },
        ],
      };
    case "gemini":
      return {
        snippets: [
          { title: "Put the token in your environment, from your secret store rather than typed into shell history:", code: exportLine },
          {
            title: "Add the server in",
            path: "~/.gemini/settings.json",
            code: json({
              mcpServers: { [SERVER_NAME]: { httpUrl: url, headers: { Authorization: `Bearer \${${TOKEN_ENV}}` } } },
            }),
          },
        ],
        note: `Gemini CLI fills \${${TOKEN_ENV}} from its environment. httpUrl, not url: url means the older SSE transport.`,
      };
    case "zed":
      return {
        snippets: [
          { title: "Put the token in your environment, from your secret store, then start Zed from that shell:", code: exportLine },
          {
            title: "Add the server under context_servers in",
            path: "~/.config/zed/settings.json",
            code: json({
              context_servers: {
                [SERVER_NAME]: { command: "sh", args: ["-c", zedBridge(url)] },
              },
            }),
          },
        ],
        note: `Zed does not fill environment variables into a remote server's headers, so this runs mcp-remote, an open-source bridge from npm, through sh, which puts ${TOKEN_ENV} into the header. It needs Node and works on macOS and Linux. The token passes through mcp-remote and sits in its process arguments, where other users on the same machine can see it with ps.`,
      };
  }
}

/** A tools/list call that proves the endpoint and token work, without an agent. */
export function curlCheck(url: string): string {
  return [
    `curl -sS ${url} \\`,
    `  -H "Authorization: Bearer $${TOKEN_ENV}" -H 'Content-Type: application/json' \\`,
    `  -H 'MCP-Protocol-Version: 2025-11-25' \\`,
    `  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'`,
  ].join("\n");
}
