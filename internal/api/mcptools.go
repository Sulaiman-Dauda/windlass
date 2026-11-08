package api

import (
	"net/http"
	"sync"

	"github.com/windlass-dev/windlass/internal/mcp"
)

// MCPTool is one row of the agent tool catalogue as the panel shows it.
type MCPTool struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Needs is "read" for a tool any token can call and "deploy" for one
	// that needs a token that can deploy.
	Needs       string `json:"needs"`
	Destructive bool   `json:"destructive"`
}

// MCPTools is the catalogue the MCP server serves, in its order. A tool
// that is not read-only needs deploy scope: TestDeployScopeMatchesReadOnly
// in internal/mcp holds the server to that.
var MCPTools = sync.OnceValue(buildMCPTools)

func buildMCPTools() []MCPTool {
	tools := mcp.Tools()
	out := make([]MCPTool, len(tools))
	for i, t := range tools {
		needs := "read"
		if !t.Annotations.ReadOnlyHint {
			needs = "deploy"
		}
		out[i] = MCPTool{Name: t.Name, Summary: t.Title, Needs: needs, Destructive: t.Annotations.DestructiveHint}
	}
	return out
}

// handleMCPTools lists what an AI agent connected to /mcp can do, for the
// panel's AI agents page. It reads no state, so any signed-in user may.
func handleMCPTools(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, MCPTools())
}

// MCPEndpoint is where an agent should connect. URL is empty when no panel
// domain is set, or Caddy is unreachable so the domain may not serve: the
// page then uses the address the browser reached it on.
type MCPEndpoint struct {
	URL string `json:"url"`
}

// handleMCPEndpoint gives any signed-in user the HTTPS endpoint under the
// panel domain. The domain's settings stay admin-only; its address is not a
// secret from someone already signed in to it.
func (a *API) handleMCPEndpoint(w http.ResponseWriter, r *http.Request) {
	hostname, err := a.Proxy.PanelDomain(r.Context())
	if err != nil {
		a.internalError(w, "get panel domain", err)
		return
	}
	var out MCPEndpoint
	if info, _ := a.Proxy.ProxyStatus(r.Context()); hostname != "" && info.Available {
		out.URL = "https://" + hostname + "/api/v1/mcp"
	}
	writeJSON(w, http.StatusOK, out)
}
