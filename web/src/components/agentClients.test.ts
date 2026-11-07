import { describe, expect, it } from "vitest";
import { CLIENTS, clientSetup, cursorInstallLink, curlCheck, mcpEndpoint, vscodeInstallLink, zedBridge } from "./agentClients";

const url = "https://panel.example/api/v1/mcp";

describe("mcpEndpoint", () => {
  it("appends the MCP path once", () => {
    expect(mcpEndpoint("https://panel.example")).toBe(url);
    expect(mcpEndpoint("https://panel.example/")).toBe(url);
  });
});

describe("clientSetup", () => {
  it("gives every client instructions with the endpoint", () => {
    for (const c of CLIENTS) {
      const s = clientSetup(c.id, url);
      const text = s.snippets.map((x) => x.code).join("\n") + (s.install?.href ?? "");
      expect(s.snippets.length, c.id).toBeGreaterThan(0);
      expect(text.includes(url) || decodeURIComponent(text).includes(url), c.id).toBe(true);
    }
  });

  it("never carries a token", () => {
    for (const c of CLIENTS) {
      const s = clientSetup(c.id, url);
      const text = [...s.snippets.map((x) => x.code), s.install?.href ?? "", s.note ?? ""].join("\n");
      expect(text, c.id).not.toMatch(/wl_pat_[0-9a-f]/);
    }
  });
});

describe("vscodeInstallLink", () => {
  it("wraps a vscode:mcp/install link whose server asks for the token", () => {
    const outer = new URL(vscodeInstallLink(url));
    expect(outer.origin).toBe("https://vscode.dev");
    expect(outer.pathname).toBe("/redirect");
    const inner = outer.searchParams.get("url")!;
    expect(inner.startsWith("vscode:mcp/install?")).toBe(true);
    const payload = JSON.parse(decodeURIComponent(inner.slice("vscode:mcp/install?".length)));
    expect(payload).toEqual({
      name: "windlass",
      type: "http",
      url,
      headers: { Authorization: "Bearer ${input:windlass-token}" },
      inputs: [{ type: "promptString", id: "windlass-token", description: "Windlass API token", password: true }],
    });
  });
});

describe("cursorInstallLink", () => {
  it("encodes the server config as base64 JSON reading the token from the environment", () => {
    const link = new URL(cursorInstallLink(url));
    expect(link.protocol).toBe("cursor:");
    expect(link.searchParams.get("name")).toBe("windlass");
    const config = JSON.parse(atob(link.searchParams.get("config")!));
    expect(config).toEqual({ url, headers: { Authorization: "Bearer ${env:WINDLASS_TOKEN}" } });
  });
});

describe("zedBridge", () => {
  it("lets sh fill the token in, as mcp-remote and Zed do not expand it", () => {
    const cfg = JSON.parse(clientSetup("zed", url).snippets[1].code);
    expect(cfg.context_servers.windlass).toEqual({ command: "sh", args: ["-c", zedBridge(url)] });
    expect(zedBridge(url)).toBe(`exec npx -y mcp-remote ${url} --header "Authorization:Bearer $WINDLASS_TOKEN"`);
    expect(zedBridge(url)).not.toContain("${");
  });
});

describe("curlCheck", () => {
  it("lists tools with the token from the environment", () => {
    const cmd = curlCheck(url);
    expect(cmd).toContain(url);
    expect(cmd).toContain('"Authorization: Bearer $WINDLASS_TOKEN"');
    expect(cmd).toContain('"method":"tools/list"');
  });
});
