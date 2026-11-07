package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/api"
	"github.com/windlass-dev/windlass/internal/config"
	"github.com/windlass-dev/windlass/internal/mcp"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// withMCP mounts the MCP server the way cmd/windlass does: on the API, with
// the root handler handed back to it for its in-process calls.
func withMCP(t *testing.T, e *testEnv) {
	t.Helper()
	srv := mcp.New(mcp.Config{Version: "test", EnvValues: api.MCPEnvValues(e.api.Projects), Audit: api.MCPAudit(e.api.Audit)})
	e.api.MCP = srv
	h, err := New(config.Config{Addr: ":0", DataDir: t.TempDir()}, e.api.Logger, e.api)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetAPI(h)
	e.handler = h
}

type mcpToolReply struct {
	status  int
	body    string
	text    string
	isError bool
	out     map[string]any
}

// mcpList returns the tool names tools/list shows for token.
func (e *testEnv) mcpList(t *testing.T, token string) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	var msg struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &msg) != nil {
		t.Fatalf("tools/list = %d %s", rec.Code, rec.Body.String())
	}
	names := make([]string, 0, len(msg.Result.Tools))
	for _, tool := range msg.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func (e *testEnv) mcpCall(t *testing.T, token, tool string, args map[string]any, cookies ...*http.Cookie) mcpToolReply {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": tool, "arguments": args,
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion":    mcp.ProtocolLatest,
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", mcp.ProtocolLatest)
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", tool)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	out := mcpToolReply{status: rec.Code, body: rec.Body.String()}
	var msg struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &msg) == nil && len(msg.Result.Content) == 1 {
		out.text = msg.Result.Content[0].Text
		out.isError = msg.Result.IsError
		out.out = msg.Result.StructuredContent
	}
	return out
}

func TestMCPThroughTheRouter(t *testing.T) {
	e := newTestEnv(t)
	withMCP(t, e)
	cookie := e.login(t)
	ctx := context.Background()

	const secret = "planted-env-value-42"
	e.prepareDeployableProject(t, cookie, "app")
	if err := e.api.Projects.SetEnv(ctx, "app", map[string]string{"DB_PASSWORD": secret, "PORT": "8080"}); err != nil {
		t.Fatal(err)
	}
	// The fake writes "log line from <container id>", so the id carries
	// the env value into the log text.
	e.agent.Containers = []agent.Container{{ID: "c-" + secret, Name: "app-web-1", ComposeProject: "app", ComposeService: "web"}}
	if rec := e.do(t, http.MethodPost, "/api/v1/projects/app/deployments", nil, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("deploy = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app", "1", "succeeded")

	read := e.createToken(t, cookie, map[string]any{"name": "agent", "scope": "read"})
	calls := map[string]map[string]any{
		"list_projects":    {},
		"get_project":      {"project": "app"},
		"list_deployments": {"project": "app"},
		"get_deployment":   {"project": "app", "number": 1},
		"get_logs":         {"project": "app"},
		"get_staging":      {"project": "app"},
		"list_backups":     {"project": "app"},
	}
	// A read token sees exactly the read tools, and the test calls each.
	if listed := e.mcpList(t, read.Token); len(listed) != len(calls) {
		t.Fatalf("read token sees %v, test covers %d tools", listed, len(calls))
	} else {
		for _, name := range listed {
			if _, ok := calls[name]; !ok {
				t.Errorf("read token sees %s", name)
			}
		}
	}
	for tool, args := range calls {
		rep := e.mcpCall(t, read.Token, tool, args)
		if rep.status != http.StatusOK || rep.isError || rep.out == nil {
			t.Errorf("%s with a read token = %d isError %v: %s", tool, rep.status, rep.isError, rep.body)
		}
		if strings.Contains(rep.body, secret) || strings.Contains(rep.body, read.Token) {
			t.Errorf("%s leaked a secret: %s", tool, rep.body)
		}
	}

	logs := e.mcpCall(t, read.Token, "get_logs", map[string]any{"project": "app"})
	if got, _ := logs.out["log"].(string); got != "log line from c-[redacted]" || logs.out["service"] != "web" {
		t.Errorf("get_logs = %s", logs.body)
	}
	if p := e.mcpCall(t, read.Token, "get_project", map[string]any{"project": "app"}); p.out["last_deployment"] == nil ||
		!strings.Contains(p.text, `"state":"running"`) {
		t.Errorf("get_project = %s", p.body)
	}
	if s := e.mcpCall(t, read.Token, "get_staging", map[string]any{"project": "app"}); s.out["has_staging"] != false {
		t.Errorf("get_staging = %s", s.body)
	}
	if rep := e.mcpCall(t, read.Token, "get_project", map[string]any{"project": "missing"}); !rep.isError ||
		!strings.Contains(rep.text, "not found") {
		t.Errorf("missing project = %s", rep.body)
	}
	// ReadEnv is a pure read: .env is unchanged and still the one SetEnv wrote.
	data, err := e.agent.FS().ReadFile(ctx, "app", ".env")
	if err != nil || !strings.Contains(string(data), secret) {
		t.Errorf(".env after the tools = %v", err)
	}

	// A session cookie alone is refused, by us rather than by RequireAuth.
	if rep := e.mcpCall(t, "", "list_projects", map[string]any{}, cookie); rep.status != http.StatusUnauthorized ||
		!strings.Contains(rep.body, "token_required") {
		t.Errorf("cookie only = %d %s", rep.status, rep.body)
	}
	if rep := e.mcpCall(t, "", "list_projects", map[string]any{}); rep.status != http.StatusUnauthorized {
		t.Errorf("no credentials = %d %s", rep.status, rep.body)
	}

	// Origin is refused whether or not the router's cross-origin check
	// lets it through.
	for _, origin := range []string{"http://example.com", "https://evil.example"} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(`{}`))
		req.Host = "example.com"
		req.Header.Set("Authorization", "Bearer "+read.Token)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		e.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("Origin %s = %d, want 403", origin, rec.Code)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		if rec := e.doBearer(t, method, "/api/v1/mcp", read.Token, nil); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /mcp = %d, want 405", method, rec.Code)
		}
	}

	// A disabled user's token stops working.
	if rec := e.do(t, http.MethodPost, "/api/v1/users", map[string]string{
		"email": "second@example.com", "password": "secondpassword123", "role": "admin",
	}, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("create second admin = %d: %s", rec.Code, rec.Body.String())
	}
	second := sessionCookie(t, e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "second@example.com", "password": "secondpassword123",
	}))
	secondToken := e.createToken(t, second, map[string]any{"name": "m", "scope": "read"})
	if rep := e.mcpCall(t, secondToken.Token, "list_projects", map[string]any{}); rep.status != http.StatusOK || rep.isError {
		t.Fatalf("second admin token before disable = %d %s", rep.status, rep.body)
	}
	u, err := e.queries.GetUserByEmail(ctx, "second@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.queries.DisableUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if rep := e.mcpCall(t, secondToken.Token, "list_projects", map[string]any{}); rep.status != http.StatusUnauthorized {
		t.Errorf("disabled user's token = %d %s, want 401", rep.status, rep.body)
	}

	// A revoked token stops working.
	if rec := e.do(t, http.MethodDelete, "/api/v1/auth/tokens/"+itoa(read.ID), nil, cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", rec.Code)
	}
	if rep := e.mcpCall(t, read.Token, "list_projects", map[string]any{}); rep.status != http.StatusUnauthorized {
		t.Errorf("revoked token = %d %s, want 401", rep.status, rep.body)
	}
}

func TestMCPDeployTokenThroughTheRouter(t *testing.T) {
	e := newTestEnv(t)
	withMCP(t, e)
	cookie := e.login(t)
	ctx := context.Background()
	e.prepareDeployableProject(t, cookie, "app")

	dep := e.createToken(t, cookie, map[string]any{"name": "deployer", "scope": "deploy"})
	read := e.createToken(t, cookie, map[string]any{"name": "reader", "scope": "read"})
	if got := e.mcpList(t, dep.Token); len(got) != len(mcp.Tools()) {
		t.Errorf("deploy token sees %v", got)
	}

	// A read token is told it cannot, and nothing is deployed.
	if rep := e.mcpCall(t, read.Token, "deploy", map[string]any{"project": "app", "confirm": true}); !rep.isError ||
		!strings.Contains(rep.text, "needs a token that can deploy") {
		t.Errorf("read token deploy = %s", rep.body)
	}
	// Without confirm the deploy token is refused before the API.
	if rep := e.mcpCall(t, dep.Token, "deploy", map[string]any{"project": "app"}); !rep.isError ||
		!strings.Contains(rep.text, "confirm") {
		t.Errorf("deploy without confirm = %s", rep.body)
	}
	if rec := e.do(t, http.MethodGet, "/api/v1/projects/app/deployments", nil, cookie); strings.Contains(rec.Body.String(), `"number"`) {
		t.Fatalf("a refused call deployed: %s", rec.Body.String())
	}

	rep := e.mcpCall(t, dep.Token, "deploy", map[string]any{"project": "app", "confirm": true})
	if rep.isError || !strings.Contains(rep.text, "get_deployment with project app and number 1") {
		t.Fatalf("deploy = %s", rep.body)
	}
	waitForStatus(t, e, cookie, "app", "1", "succeeded")
	if rep := e.mcpCall(t, dep.Token, "rollback", map[string]any{"project": "app", "number": 1, "confirm": true}); rep.isError {
		t.Errorf("rollback = %s", rep.body)
	}
	waitForStatus(t, e, cookie, "app", "2", "succeeded")
	if rep := e.mcpCall(t, dep.Token, "restart", map[string]any{"project": "app", "confirm": true}); rep.isError ||
		rep.out["result"] != "restarted" {
		t.Errorf("restart = %s", rep.body)
	}
	backup := e.mcpCall(t, dep.Token, "create_backup", map[string]any{"project": "app"})
	// No destination, so no request body: the route must still get one.
	if backup.isError || backup.out["result"] != "backed up" {
		t.Errorf("create_backup = %s", backup.body)
	}
	// No staging: promote says so.
	if rep := e.mcpCall(t, dep.Token, "promote", map[string]any{"project": "app", "confirm": true}); !rep.isError ||
		!strings.Contains(rep.text, "has no staging") {
		t.Errorf("promote = %s", rep.body)
	}
	// Still refused for what the scope does not cover, at the router.
	if rec := e.doBearer(t, http.MethodPost, "/api/v1/projects/app/actions/stop", dep.Token, nil); rec.Code != http.StatusForbidden {
		t.Errorf("deploy token stop = %d", rec.Code)
	}

	// Each successful write has the route's line and an mcp.call line, both
	// naming the token.
	entries, err := e.queries.ListAudit(ctx, db.ListAuditParams{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	route := map[string]int{}
	viaMCP := map[string]int{}
	for _, a := range entries {
		if !a.TokenID.Valid || a.TokenID.Int64 != dep.ID {
			continue
		}
		if a.Action == "mcp.call" {
			var d struct {
				Tool string `json:"tool"`
			}
			_ = json.Unmarshal([]byte(a.Detail.String), &d)
			if a.ResourceID.String != "app" || a.Ip.String == "" {
				t.Errorf("mcp.call line %+v", a)
			}
			viaMCP[d.Tool]++
			continue
		}
		route[a.Action]++
	}
	want := map[string]string{"deploy": "deployment.create", "rollback": "deployment.rollback",
		"restart": "project.restart", "create_backup": "backup.create"}
	for tool, action := range want {
		if viaMCP[tool] != 1 || route[action] != 1 {
			t.Errorf("%s: %d mcp.call lines and %d %s lines, want 1 and 1", tool, viaMCP[tool], route[action], action)
		}
	}
	if len(viaMCP) != len(want) {
		t.Errorf("mcp.call lines %v, want one each for %v", viaMCP, want)
	}
}

// Values written inline in a Compose file are scrubbed like .env ones, a
// short one under a secret-named key included, and a Compose file that
// will not parse withholds the log rather than scrub it with half the list.
func TestMCPScrubsInlineComposeValues(t *testing.T) {
	e := newTestEnv(t)
	withMCP(t, e)
	cookie := e.login(t)
	ctx := context.Background()
	e.prepareDeployableProject(t, cookie, "app")
	compose := "services:\n  web:\n    image: nginx\n    environment:\n      SMTP_PASS: inline-sekret-77\n      DB_PWD: k7q2\n" +
		"configs:\n  app:\n    content: |\n      api_secret = cfg-sekret-5\n"
	if err := e.agent.FS().WriteFile(ctx, "app", "compose.yaml", []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	e.agent.Containers = []agent.Container{{ID: "c-inline-sekret-77-k7q2-cfg-sekret-5", Name: "app-web-1", ComposeProject: "app", ComposeService: "web"}}
	read := e.createToken(t, cookie, map[string]any{"name": "agent", "scope": "read"})

	logs := e.mcpCall(t, read.Token, "get_logs", map[string]any{"project": "app"})
	if got, _ := logs.out["log"].(string); got != "log line from c-[redacted]-[redacted]-[redacted]" {
		t.Errorf("get_logs = %s", logs.body)
	}

	if err := e.agent.FS().WriteFile(ctx, "app", "compose.override.yaml", []byte("services: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logs = e.mcpCall(t, read.Token, "get_logs", map[string]any{"project": "app"})
	if strings.Contains(logs.body, "sekret") || !logs.isError || !strings.Contains(logs.text, "withheld") {
		t.Errorf("get_logs with a broken compose file = %s", logs.body)
	}
}
