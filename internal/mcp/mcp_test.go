package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var testToken = "wl_pat_" + strings.Repeat("ab12", 15)

// The planted secret: long enough to be scrubbed, and placed in every
// field a tool could pass through.
const plantedSecret = "s3cr3t-db-password" //nolint:gosec // G101: planted test value

// fakeAPI stands in for the /api/v1 router. It checks the bearer token the
// way auth.Middleware does and answers the routes the tools call.
type fakeAPI struct {
	mu       sync.Mutex
	paths    []string
	projects int
	logLines []string
	events   int
	doneMsg  string // the last event's message; "failed" when empty
	staging  bool

	// What /auth/tokens/current reports, and whether the write routes
	// accept the token. refuseWrites makes them 403 even so, as the router
	// does when the owner is demoted between two calls.
	scope        string
	role         string
	canDeploy    bool
	refuseWrites bool
	tokenErr     bool
	// restartDelay is how long a restart takes; each one sends whether it
	// was cancelled on restarts, if set.
	restartDelay time.Duration
	restarts     chan bool
	restartFails bool
	backupFails  bool
	backupBody   string
	// madeBackups, when set, makes each backup POST add a manual backup to
	// the list, as the real route does.
	madeBackups *[]map[string]any
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		projects: 2,
		logLines: []string{
			"starting app",
			"DATABASE_URL=postgres://app:" + plantedSecret + "@db/app",
			"token " + testToken + " used",
			"line one of the key", "-----BEGIN KEY-----", "MIIBkeyline",
			"port is 8080",
		},
		events:  3,
		staging: true,
		scope:   "read",
		role:    "member",
	}
}

// deployFake is a fake whose token can deploy.
func deployFake() *fakeAPI {
	f := newFakeAPI()
	f.scope, f.canDeploy = "deploy", true
	return f
}

func (f *fakeAPI) posts() []string {
	var out []string
	for _, p := range f.seen() {
		if !strings.HasPrefix(p, "GET ") {
			out = append(out, p)
		}
	}
	return out
}

// writeRoutes are the routes a deploy token may POST to, as the fake
// serves them.
var writeRoutes = map[string]bool{
	"/projects/web/deployments":                   true,
	"/projects/web/deployments/2/rollback":        true,
	"/projects/web/actions/restart":               true,
	"/projects/web-staging/deployments/5/promote": true,
	"/projects/web-staging/deployments/4/promote": true,
	"/projects/web/backups":                       true,
}

func (f *fakeAPI) serveWrite(w http.ResponseWriter, r *http.Request, path string) {
	if !f.canDeploy || f.refuseWrites {
		writeTestJSON(w, 403, map[string]any{"error": map[string]string{"code": "forbidden", "message": "insufficient role"}})
		return
	}
	switch {
	case path == "/projects/web/deployments":
		writeTestJSON(w, 201, map[string]any{"id": 10, "number": 3, "status": "queued", "trigger": "manual"})
	case path == "/projects/web/deployments/2/rollback":
		writeTestJSON(w, 201, map[string]any{"id": 11, "number": 4, "status": "queued", "trigger": "rollback"})
	case strings.HasPrefix(path, "/projects/web-staging/deployments/"):
		writeTestJSON(w, 201, map[string]any{"id": 12, "number": 7, "status": "queued", "trigger": "promote"})
	case path == "/projects/web/actions/restart":
		cancelled := false
		select {
		case <-time.After(f.restartDelay):
		case <-r.Context().Done():
			cancelled = true
		}
		if f.restarts != nil {
			f.restarts <- cancelled
		}
		if f.restartFails {
			writeTestJSON(w, 502, map[string]any{"error": map[string]string{"code": "compose_failed",
				"message": "compose restart: bad value " + plantedSecret}})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case path == "/projects/web/backups":
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.backupBody = string(b)
		f.mu.Unlock()
		if f.backupFails {
			writeTestJSON(w, 502, map[string]any{"id": 3, "status": "failed", "error": "pg_dump: " + plantedSecret})
			return
		}
		if f.madeBackups != nil {
			f.mu.Lock()
			*f.madeBackups = append(*f.madeBackups, map[string]any{"id": 100 + len(*f.madeBackups), "kind": "manual",
				"status": "done", "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
			f.mu.Unlock()
		}
		writeTestJSON(w, 201, map[string]any{"id": 3, "status": "succeeded", "destination": "local", "size_bytes": 1024})
	}
}

func (f *fakeAPI) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func writeTestJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSSE(w http.ResponseWriter, typ string, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b)
	w.(http.Flusher).Flush()
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.RequestURI())
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		writeTestJSON(w, 401, map[string]any{"error": map[string]string{"code": "unauthorized", "message": "sign in"}})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if r.Method == http.MethodPost && writeRoutes[path] {
		f.serveWrite(w, r, path)
		return
	}
	if r.Method != http.MethodGet {
		writeTestJSON(w, 405, map[string]any{"error": map[string]string{"code": "method_not_allowed"}})
		return
	}
	project := map[string]any{
		"id": 1, "name": "web", "source": "git",
		"git_repo": "https://bot:ghp_" + plantedSecret + "@github.com/acme/web.git", "git_branch": "main",
	}
	deployment := map[string]any{
		"id": 9, "number": 2, "status": "failed", "created_at": "2026-11-06T10:00:00Z",
		"finished_at": "2026-11-06T10:01:00Z",
		"error":       "compose up: invalid value " + plantedSecret,
	}
	switch path {
	case "/auth/tokens/current":
		if f.tokenErr {
			writeTestJSON(w, 500, map[string]any{"error": map[string]string{"code": "internal", "message": "database"}})
			return
		}
		writeTestJSON(w, 200, map[string]any{"id": 1, "scope": f.scope, "can_deploy": f.canDeploy,
			"user": map[string]any{"id": 1, "role": f.role}})
	case "/projects":
		list := make([]map[string]any, 0, f.projects)
		for i := 0; i < f.projects; i++ {
			list = append(list, map[string]any{"id": i, "name": fmt.Sprintf("p%d", i), "git_repo": project["git_repo"]})
		}
		writeTestJSON(w, 200, list)
	case "/projects/web":
		writeTestJSON(w, 200, project)
	case "/projects/web/services":
		writeTestJSON(w, 200, map[string]any{"services": []map[string]any{{"service": "app", "state": "running"}}})
	case "/projects/multi/services":
		writeTestJSON(w, 200, map[string]any{"services": []map[string]any{{"service": "app"}, {"service": "db"}}})
	case "/projects/multi":
		writeTestJSON(w, 200, map[string]any{"name": "multi"})
	case "/projects/web/domains":
		writeTestJSON(w, 200, []map[string]any{{"hostname": "web.example.com", "service": "app", "status": "active"}})
	case "/projects/web/deployments":
		writeTestJSON(w, 200, []map[string]any{deployment})
	case "/projects/web/deployments/2":
		writeTestJSON(w, 200, deployment)
	case "/projects/web/deployments/2/events":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		for i := 1; i <= f.events; i++ {
			writeSSE(w, "deployment.log", map[string]any{"seq": i, "type": "log", "message": fmt.Sprintf("step %d %s", i, plantedSecret)})
		}
		done := f.doneMsg
		if done == "" {
			done = "failed"
		}
		writeSSE(w, "deployment.done", map[string]any{"seq": f.events + 1, "type": "done", "message": done})
	case "/projects/web/logs":
		if r.URL.Query().Get("follow") != "false" {
			writeTestJSON(w, 400, map[string]any{"error": map[string]string{"message": "the tools must not follow"}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		for _, l := range f.logLines {
			writeSSE(w, "log", map[string]string{"stream": "stdout", "text": l})
		}
	case "/projects/web/staging":
		if !f.staging {
			writeTestJSON(w, 404, map[string]any{"error": map[string]string{"code": "not_found", "message": "project has no staging"}})
			return
		}
		writeTestJSON(w, 200, map[string]any{"live": "web", "staging": "web-staging", "drift": []any{},
			"promotable": map[string]any{"number": 5, "status": "succeeded"}})
	case "/projects/web/backups":
		list := []map[string]any{{"id": 1, "kind": "volume", "destination": "local", "status": "failed",
			"error": "pg_dump: " + plantedSecret}}
		if f.madeBackups != nil {
			f.mu.Lock()
			list = append(list, *f.madeBackups...)
			f.mu.Unlock()
		}
		writeTestJSON(w, 200, list)
	case "/projects/web/backups/schedule":
		writeTestJSON(w, 200, map[string]any{"interval": "daily", "destination": "local", "retention_count": 7, "enabled": true})
	default:
		writeTestJSON(w, 404, map[string]any{"error": map[string]string{"code": "not_found", "message": "project not found"}})
	}
}

func envValues(ctx context.Context, project string) (map[string]string, error) {
	return map[string]string{
		"DB_PASSWORD": plantedSecret,
		"PORT":        "8080",
		"DEBUG":       "true",
		"KEY":         "-----BEGIN KEY-----\nMIIBkeyline\n-----END KEY-----",
	}, nil
}

func newTestServer(t *testing.T, api *fakeAPI) *Server {
	t.Helper()
	s := New(Config{Version: "test", EnvValues: envValues})
	s.SetAPI(api)
	return s
}

type rpcReply struct {
	status int
	header http.Header
	body   string
	msg    struct {
		ID     json.RawMessage `json:"id"`
		Result map[string]json.RawMessage
		Error  *struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
	}
}

// send posts body with headers; headers with an empty value are omitted.
func send(t *testing.T, s http.Handler, body string, headers map[string]string) rpcReply {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	out := rpcReply{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out.msg)
	}
	return out
}

func modernBody(method string, params map[string]any) string {
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = map[string]any{
		metaProtocolVersion: ProtocolLatest,
		metaClientCaps:      map[string]any{},
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	return string(b)
}

func modernHeaders(method, name string) map[string]string {
	h := map[string]string{"MCP-Protocol-Version": ProtocolLatest, "Mcp-Method": method}
	if name != "" {
		h["Mcp-Name"] = name
	}
	return h
}

func legacyBody(method string, params map[string]any) string {
	m := map[string]any{"jsonrpc": "2.0", "id": 7, "method": method}
	if params != nil {
		m["params"] = params
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// callTool runs a tool on the modern protocol and returns its result.
func callTool(t *testing.T, s http.Handler, name string, args map[string]any) (structured map[string]any, text string, isError bool) {
	t.Helper()
	rep := send(t, s, modernBody("tools/call", map[string]any{"name": name, "arguments": args}), modernHeaders("tools/call", name))
	if rep.status != 200 || rep.msg.Error != nil {
		t.Fatalf("%s: status %d: %s", name, rep.status, rep.body)
	}
	var content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(rep.msg.Result["content"], &content)
	if len(content) != 1 || content[0].Type != "text" {
		t.Fatalf("%s: content %s", name, rep.msg.Result["content"])
	}
	_ = json.Unmarshal(rep.msg.Result["isError"], &isError)
	if raw, ok := rep.msg.Result["structuredContent"]; ok {
		if err := json.Unmarshal(raw, &structured); err != nil {
			t.Fatalf("%s: structuredContent is not an object: %s", name, raw)
		}
		if string(mustCompact(raw)) != string(mustCompact(json.RawMessage(content[0].Text))) {
			t.Errorf("%s: text and structuredContent differ", name)
		}
	}
	var rt string
	_ = json.Unmarshal(rep.msg.Result["resultType"], &rt)
	if rt != "complete" {
		t.Errorf("%s: resultType %q", name, rt)
	}
	return structured, content[0].Text, isError
}

func mustCompact(raw json.RawMessage) []byte {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	b, _ := json.Marshal(v)
	return b
}

func TestRefusesCookieOnly(t *testing.T) {
	s := newTestServer(t, newFakeAPI())
	for _, auth := range []string{"", "Bearer", "Bearer some-oauth-token", "Basic dXNlcjpwYXNz"} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(legacyBody("tools/list", nil)))
		req.AddCookie(&http.Cookie{Name: "windlass_session", Value: "a-valid-looking-session"}) //nolint:gosec // G124: a request cookie in a test
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "token_required") {
			t.Errorf("Authorization %q with a cookie = %d %s, want 401 token_required", auth, rec.Code, rec.Body.String())
		}
	}
}

func TestOriginIs403(t *testing.T) {
	s := newTestServer(t, newFakeAPI())
	for _, origin := range []string{"https://evil.example", "http://localhost:3000", "null", ""} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(legacyBody("tools/list", nil)))
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header["Origin"] = []string{origin}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("Origin %q = %d, want 403", origin, rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("Origin %q got a CORS header", origin)
		}
	}
}

func TestGetAndDeleteAre405(t *testing.T) {
	s := newTestServer(t, newFakeAPI())
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		req := httptest.NewRequest(method, "/api/v1/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Mcp-Session-Id", "abc")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodPost {
			t.Errorf("%s = %d Allow %q, want 405 POST", method, rec.Code, rec.Header().Get("Allow"))
		}
	}
}

func TestLatestNeedsHeaders(t *testing.T) {
	s := newTestServer(t, newFakeAPI())
	list := modernBody("tools/list", nil)
	call := modernBody("tools/call", map[string]any{"name": "list_projects", "arguments": map[string]any{}})

	cases := []struct {
		name    string
		body    string
		headers map[string]string
		status  int
		code    int
	}{
		{"no version header", list, map[string]string{"Mcp-Method": "tools/list"}, 400, codeHeaderMismatch},
		{"version header differs", list, map[string]string{"MCP-Protocol-Version": ProtocolLegacy, "Mcp-Method": "tools/list"}, 400, codeHeaderMismatch},
		{"no method header", list, map[string]string{"MCP-Protocol-Version": ProtocolLatest}, 400, codeHeaderMismatch},
		{"method header differs", list, modernHeaders("tools/call", ""), 400, codeHeaderMismatch},
		{"no name header", call, modernHeaders("tools/call", ""), 400, codeHeaderMismatch},
		{"name header differs", call, modernHeaders("tools/call", "get_logs"), 400, codeHeaderMismatch},
		{"bad base64 name", call, modernHeaders("tools/call", "=?base64?!!!?="), 400, codeHeaderMismatch},
		{"header but no _meta", legacyBody("tools/list", nil), modernHeaders("tools/list", ""), 400, codeInvalidParams},
		{"no client capabilities",
			`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
			modernHeaders("tools/list", ""), 400, codeInvalidParams},
		{"unknown version", legacyBody("tools/list", nil), map[string]string{"MCP-Protocol-Version": "2030-01-01"}, 400, codeUnsupportedVersion},
		{"ping is gone", modernBody("ping", nil), modernHeaders("ping", ""), 404, codeMethodNotFound},
		{"initialize-free method unknown", modernBody("resources/list", nil), modernHeaders("resources/list", ""), 404, codeMethodNotFound},
	}
	for _, c := range cases {
		rep := send(t, s, c.body, c.headers)
		if rep.status != c.status || rep.msg.Error == nil || rep.msg.Error.Code != c.code {
			t.Errorf("%s: %d %s, want %d code %d", c.name, rep.status, rep.body, c.status, c.code)
		}
	}

	// The version error says what we support.
	rep := send(t, s, legacyBody("tools/list", nil), map[string]string{"MCP-Protocol-Version": "2030-01-01"})
	var data struct {
		Supported []string `json:"supported"`
		Requested string   `json:"requested"`
	}
	_ = json.Unmarshal(rep.msg.Error.Data, &data)
	if len(data.Supported) != 2 || data.Supported[0] != ProtocolLatest || data.Requested != "2030-01-01" {
		t.Errorf("unsupported version data = %s", rep.msg.Error.Data)
	}

	// With the headers right: discover, list and a base64 Mcp-Name call.
	rep = send(t, s, modernBody("server/discover", nil), modernHeaders("server/discover", ""))
	if rep.status != 200 || rep.msg.Error != nil {
		t.Fatalf("discover: %d %s", rep.status, rep.body)
	}
	for _, key := range []string{"supportedVersions", "capabilities", "ttlMs", "cacheScope", "resultType", "_meta"} {
		if _, ok := rep.msg.Result[key]; !ok {
			t.Errorf("discover result has no %s: %s", key, rep.body)
		}
	}
	if !strings.Contains(string(rep.msg.Result["_meta"]), metaServerInfo) {
		t.Errorf("discover _meta lacks serverInfo: %s", rep.msg.Result["_meta"])
	}
	rep = send(t, s, list, modernHeaders("tools/list", ""))
	if rep.status != 200 || string(rep.msg.Result["resultType"]) != `"complete"` || rep.msg.Result["ttlMs"] == nil {
		t.Errorf("tools/list: %d %s", rep.status, rep.body)
	}
	encoded := "=?base64?" + base64.StdEncoding.EncodeToString([]byte("list_projects")) + "?="
	rep = send(t, s, call, modernHeaders("tools/call", encoded))
	if rep.status != 200 || rep.msg.Error != nil {
		t.Errorf("base64 Mcp-Name: %d %s", rep.status, rep.body)
	}
	if rep.header.Get("Mcp-Session-Id") != "" {
		t.Errorf("modern response minted a session")
	}
}

func TestLegacyInitializeHasNoSession(t *testing.T) {
	s := newTestServer(t, newFakeAPI())
	for _, version := range []string{ProtocolLegacy, "2025-06-18", "2024-11-05"} {
		rep := send(t, s, legacyBody("initialize", map[string]any{
			"protocolVersion": version, "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "test", "version": "1"},
		}), nil)
		if rep.status != 200 || rep.msg.Error != nil {
			t.Fatalf("initialize %s: %d %s", version, rep.status, rep.body)
		}
		if rep.header.Get("Mcp-Session-Id") != "" {
			t.Errorf("initialize %s minted a session", version)
		}
		want := version
		if version == "2024-11-05" {
			want = ProtocolLegacy
		}
		if string(rep.msg.Result["protocolVersion"]) != `"`+want+`"` {
			t.Errorf("initialize %s agreed %s, want %s", version, rep.msg.Result["protocolVersion"], want)
		}
		if _, ok := rep.msg.Result["resultType"]; ok {
			t.Errorf("legacy result carries resultType")
		}
		if string(rep.msg.ID) != "7" {
			t.Errorf("id %s, want 7", rep.msg.ID)
		}
	}

	rep := send(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, map[string]string{"MCP-Protocol-Version": ProtocolLegacy})
	if rep.status != http.StatusAccepted || rep.body != "" {
		t.Errorf("initialized notification = %d %q, want 202 and no body", rep.status, rep.body)
	}
	rep = send(t, s, legacyBody("ping", nil), map[string]string{"MCP-Protocol-Version": ProtocolLegacy})
	if rep.status != 200 || string(rep.msg.Result["resultType"]) != "" || rep.msg.Error != nil {
		t.Errorf("legacy ping = %d %s", rep.status, rep.body)
	}
	// After the handshake each request stands alone, with or without the header.
	for _, h := range []map[string]string{nil, {"MCP-Protocol-Version": ProtocolLegacy}} {
		rep = send(t, s, legacyBody("tools/list", nil), h)
		var tools []Tool
		if err := json.Unmarshal(rep.msg.Result["tools"], &tools); err != nil || len(tools) != 7 {
			t.Errorf("legacy tools/list = %d %s", rep.status, rep.body)
		}
	}
	rep = send(t, s, legacyBody("tools/call", map[string]any{"name": "list_projects"}), map[string]string{"MCP-Protocol-Version": ProtocolLegacy})
	if rep.status != 200 || string(rep.msg.Result["isError"]) != "false" {
		t.Errorf("legacy tools/call = %d %s", rep.status, rep.body)
	}
	// Unknown methods are JSON-RPC errors on HTTP 200 in the old revision.
	rep = send(t, s, legacyBody("resources/list", nil), nil)
	if rep.status != 200 || rep.msg.Error == nil || rep.msg.Error.Code != codeMethodNotFound {
		t.Errorf("legacy unknown method = %d %s", rep.status, rep.body)
	}
}

func TestProtocolErrors(t *testing.T) {
	s := newTestServer(t, newFakeAPI())
	cases := []struct {
		name, body string
		status     int
		code       int
	}{
		{"not json", `{"jsonrpc":`, 400, codeParseError},
		{"batch", `[` + legacyBody("tools/list", nil) + `]`, 400, codeInvalidRequest},
		{"no jsonrpc", `{"id":1,"method":"tools/list"}`, 400, codeInvalidRequest},
		{"object id", `{"jsonrpc":"2.0","id":{},"method":"tools/list"}`, 400, codeInvalidRequest},
		{"params array", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":[]}`, 400, codeInvalidParams},
		{"unknown tool", legacyBody("tools/call", map[string]any{"name": "delete_project"}), 200, codeInvalidParams},
		{"arguments not object", legacyBody("tools/call", map[string]any{"name": "get_project", "arguments": "web"}), 200, codeInvalidParams},
		{"cursor", legacyBody("tools/list", map[string]any{"cursor": "x"}), 200, codeInvalidParams},
	}
	for _, c := range cases {
		rep := send(t, s, c.body, nil)
		if rep.status != c.status || rep.msg.Error == nil || rep.msg.Error.Code != c.code {
			t.Errorf("%s: %d %s, want %d code %d", c.name, rep.status, rep.body, c.status, c.code)
		}
		// JSON-RPC: an error whose request id is unknown still has one, null.
		if !strings.Contains(rep.body, `"id":`) {
			t.Errorf("%s: no id in %s", c.name, rep.body)
		}
	}
	// Big bodies stop at 1 MiB.
	rep := send(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"x":"`+strings.Repeat("a", maxBody)+`"}}`, nil)
	if rep.status != http.StatusRequestEntityTooLarge {
		t.Errorf("big body = %d", rep.status)
	}
}

func TestToolArgumentErrorsAreResults(t *testing.T) {
	s := newTestServer(t, newFakeAPI())
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"get_project", map[string]any{}, "project is required"},
		{"get_project", map[string]any{"project": "../etc"}, "not a valid project name"},
		{"get_project", map[string]any{"project": 5}, "must be a string"},
		{"get_project", map[string]any{"project": "web", "env": true}, "unknown argument env"},
		{"list_projects", map[string]any{"all": true}, "takes no arguments"},
		{"get_logs", map[string]any{"project": "web", "tail": 501}, "between 1 and 500"},
		{"get_logs", map[string]any{"project": "web", "tail": 2.5}, "whole number"},
		{"get_logs", map[string]any{"project": "multi"}, "name one in service: app, db"},
		{"get_project", map[string]any{"project": "nope"}, "not found"},
	}
	for _, c := range cases {
		_, text, isError := callTool(t, s, c.tool, c.args)
		if !isError || !strings.Contains(text, c.want) {
			t.Errorf("%s %v = isError %v %q, want an error containing %q", c.tool, c.args, isError, text, c.want)
		}
	}
	// 5.0 is a whole number.
	if _, text, isError := callTool(t, s, "get_logs", map[string]any{"project": "web", "tail": json.RawMessage("5.0")}); isError {
		t.Errorf("tail 5.0 refused: %s", text)
	}
}

// readTools are the tools a read token sees.
var readTools = map[string]bool{
	"list_projects": true, "get_project": true, "list_deployments": true, "get_deployment": true,
	"get_logs": true, "get_staging": true, "list_backups": true,
}

// destructiveTools change what is running and need confirm.
var destructiveTools = map[string]bool{"deploy": true, "restart": true, "rollback": true, "promote": true, "create_backup": true}

func TestNoToolReturnsEnv(t *testing.T) {
	api := deployFake()
	s := newTestServer(t, api)
	calls := map[string]map[string]any{
		"list_projects":    {},
		"get_project":      {"project": "web"},
		"list_deployments": {"project": "web"},
		"get_deployment":   {"project": "web", "number": 2},
		"get_logs":         {"project": "web"},
		"get_staging":      {"project": "web"},
		"list_backups":     {"project": "web"},
		"deploy":           {"project": "web", "confirm": true},
		"restart":          {"project": "web", "confirm": true},
		"rollback":         {"project": "web", "number": 2, "confirm": true},
		"promote":          {"project": "web", "confirm": true},
		"create_backup":    {"project": "web", "confirm": true},
	}
	if len(calls) != len(Tools()) {
		t.Fatalf("test covers %d tools, catalogue has %d", len(calls), len(Tools()))
	}
	for _, tool := range Tools() {
		args, ok := calls[tool.Name]
		if !ok {
			t.Fatalf("no call for %s", tool.Name)
		}
		_, text, isError := callTool(t, s, tool.Name, args)
		if isError {
			t.Errorf("%s failed: %s", tool.Name, text)
		}
		if strings.Contains(text, plantedSecret) || strings.Contains(text, testToken) {
			t.Errorf("%s leaked a secret: %s", tool.Name, text)
		}
	}
	for _, p := range api.seen() {
		if strings.Contains(p, "/env") || strings.Contains(p, "/files") {
			t.Errorf("a tool called %s", p)
		}
		method, path, _ := strings.Cut(p, " ")
		if method != http.MethodGet && (method != http.MethodPost || !writeRoutes[strings.TrimPrefix(path, "/api/v1")]) {
			t.Errorf("a tool sent %s", p)
		}
	}
	if got := len(api.posts()); got != 5 {
		t.Errorf("%d writes sent, want one per write tool: %v", got, api.posts())
	}
	for _, tool := range Tools() {
		if strings.Contains(tool.Name, "env") || strings.Contains(string(tool.InputSchema), `"env`) {
			t.Errorf("%s looks like an env tool", tool.Name)
		}
	}
}

func TestLogsScrubbed(t *testing.T) {
	s := newTestServer(t, newFakeAPI())
	out, text, isError := callTool(t, s, "get_logs", map[string]any{"project": "web"})
	if isError {
		t.Fatalf("get_logs: %s", text)
	}
	log, _ := out["log"].(string)
	for _, leak := range []string{plantedSecret, testToken, "MIIBkeyline", "-----BEGIN KEY-----"} {
		if strings.Contains(log, leak) {
			t.Errorf("log still has %q:\n%s", leak, log)
		}
	}
	for _, keep := range []string{"starting app", "port is 8080", "postgres://app:[redacted]@db/app", "token [redacted] used"} {
		if !strings.Contains(log, keep) {
			t.Errorf("log lost %q:\n%s", keep, log)
		}
	}
	if out["service"] != "app" {
		t.Errorf("service = %v, want the only service, app", out["service"])
	}

	// Without env values the logs are withheld, never sent raw.
	for name, ev := range map[string]func(context.Context, string) (map[string]string, error){
		"nil":   nil,
		"error": func(context.Context, string) (map[string]string, error) { return nil, errors.New("disk") },
	} {
		s := New(Config{EnvValues: ev})
		s.SetAPI(newFakeAPI())
		_, text, isError := callTool(t, s, "get_logs", map[string]any{"project": "web"})
		if !isError || strings.Contains(text, plantedSecret) || !strings.Contains(text, "withheld") {
			t.Errorf("EnvValues %s: isError %v %s", name, isError, text)
		}
		out, text, _ := callTool(t, s, "get_deployment", map[string]any{"project": "web", "number": 2})
		if strings.Contains(text, plantedSecret) || out["events_withheld"] == nil {
			t.Errorf("EnvValues %s: deployment %s", name, text)
		}
		_, text, _ = callTool(t, s, "list_backups", map[string]any{"project": "web"})
		if strings.Contains(text, plantedSecret) {
			t.Errorf("EnvValues %s: backups %s", name, text)
		}
	}
}

func TestScrubberRules(t *testing.T) {
	sc := newScrubber(map[string]string{
		"A": "short", "B": "abcdef", "C": "abcdefgh", "D": "  padded-value  ", "E": "",
	})
	got := sc.scrub("short abcdefgh abcdef padded-value " + testToken)
	want := "short [redacted] [redacted] [redacted] [redacted]"
	if got != want {
		t.Errorf("scrub = %q, want %q", got, want)
	}
	// A key that names a secret lowers the floor to 4, past a prefix
	// ending in a colon, and settings such as true are left alone.
	sc = newScrubber(map[string]string{
		"DB_PASSWORD": "pw12", "compose.yaml#0:API_KEY": "k9z8", "AUTH_TOKEN": "true", "PORT": "8080",
		"KEY": "abc", "compose.yaml#1:REDIS_PASS": "r3d1s", "secrets.yaml#2:MODE": "fast",
	})
	got = sc.scrub("pw12 k9z8 true 8080 abc r3d1s fast")
	want = "[redacted] [redacted] true 8080 abc [redacted] fast"
	if got != want {
		t.Errorf("named scrub = %q, want %q", got, want)
	}
	for in, want := range map[string]string{ //nolint:gosec // G101: fake credentials the test strips
		"https://x-access-token:ghp_abc@github.com/a/b.git": "https://[redacted]@github.com/a/b.git",
		"https://ghp_abc@github.com/a/b.git":                "https://[redacted]@github.com/a/b.git",
		"ssh://git@github.com/a/b.git":                      "ssh://git@github.com/a/b.git",
		"git@github.com:a/b.git":                            "git@github.com:a/b.git",
		"https://github.com/a/b.git":                        "https://github.com/a/b.git",
		"https://github.com/a/b@v1":                         "https://github.com/a/b@v1",
	} {
		if got := stripURLCredentials(in); got != want {
			t.Errorf("stripURLCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLimitsAreReported(t *testing.T) {
	api := newFakeAPI()
	api.projects = MaxListItems + 50
	api.logLines = nil
	for i := 0; i < MaxLogLines+100; i++ {
		api.logLines = append(api.logLines, fmt.Sprintf("line %04d", i))
	}
	api.events = 2000
	s := newTestServer(t, api)

	out, _, _ := callTool(t, s, "list_projects", map[string]any{})
	if list, _ := out["projects"].([]any); len(list) != MaxListItems || out["truncated"] != true || out["total"] != float64(MaxListItems+50) {
		t.Errorf("list_projects: %d items truncated %v total %v", len(list), out["truncated"], out["total"])
	}

	out, _, _ = callTool(t, s, "get_logs", map[string]any{"project": "web", "tail": MaxLogLines})
	log, _ := out["log"].(string)
	lines := strings.Split(log, "\n")
	if len(lines) != MaxLogLines || out["truncated"] != true || lines[len(lines)-1] != fmt.Sprintf("line %04d", MaxLogLines+99) {
		t.Errorf("get_logs: %d lines truncated %v last %q", len(lines), out["truncated"], lines[len(lines)-1])
	}

	// A log past MaxLogBytes keeps the newest bytes.
	api.logLines = []string{strings.Repeat("x", MaxLogBytes), "newest"}
	out, _, _ = callTool(t, s, "get_logs", map[string]any{"project": "web"})
	log, _ = out["log"].(string)
	if len(log) > MaxLogBytes || !strings.HasSuffix(log, "newest") || out["truncated"] != true {
		t.Errorf("get_logs bytes: %d bytes truncated %v", len(log), out["truncated"])
	}

	out, _, _ = callTool(t, s, "get_deployment", map[string]any{"project": "web", "number": 2})
	events, _ := out["events"].([]any)
	if len(events) == 0 || len(events) > MaxLogLines || out["events_truncated"] != true {
		t.Errorf("get_deployment: %d events truncated %v", len(events), out["events_truncated"])
	}
	if last, _ := events[len(events)-1].(map[string]any); last["type"] != "done" {
		t.Errorf("get_deployment dropped the newest event: %v", last)
	}
	// Newest event alone past MaxLogBytes: the end of it, where the reason is.
	api.doneMsg = strings.Repeat("x", MaxLogBytes) + " the reason"
	out, _, _ = callTool(t, s, "get_deployment", map[string]any{"project": "web", "number": 2})
	events, _ = out["events"].([]any)
	if len(events) != 1 || out["events_truncated"] != true {
		t.Fatalf("get_deployment big event: %d events truncated %v", len(events), out["events_truncated"])
	}
	if msg, _ := events[0].(map[string]any)["message"].(string); len(msg) > MaxLogBytes || !strings.HasSuffix(msg, " the reason") {
		t.Errorf("get_deployment big event: %d bytes, ends %q", len(msg), msg[max(0, len(msg)-20):])
	}
	api.doneMsg = ""

	out, _, _ = callTool(t, s, "list_deployments", map[string]any{"project": "web", "limit": 1})
	if out["returned"] != float64(1) || out["truncated"] != false {
		t.Errorf("list_deployments: %v", out)
	}
}

func TestEveryToolHasAllAnnotations(t *testing.T) {
	tools := Tools()
	if len(tools) != 12 {
		t.Fatalf("%d tools, want 12", len(tools))
	}
	b, _ := json.Marshal(tools)
	var raw []map[string]json.RawMessage
	_ = json.Unmarshal(b, &raw)
	seen := map[string]bool{}
	for i, tool := range tools {
		if seen[tool.Name] {
			t.Errorf("duplicate tool %s", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Title == "" || tool.Description == "" {
			t.Errorf("%s has no title or description", tool.Name)
		}
		var ann map[string]any
		_ = json.Unmarshal(raw[i]["annotations"], &ann)
		for _, key := range []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
			if _, ok := ann[key].(bool); !ok {
				t.Errorf("%s: annotation %s is not set", tool.Name, key)
			}
		}
		var schema struct {
			Type                 string                     `json:"type"`
			AdditionalProperties *bool                      `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil || schema.Type != "object" ||
			schema.AdditionalProperties == nil || *schema.AdditionalProperties {
			t.Errorf("%s: input schema %s", tool.Name, tool.InputSchema)
		}
		_, hasConfirm := schema.Properties["confirm"]
		confirmRequired := false
		for _, r := range schema.Required {
			confirmRequired = confirmRequired || r == "confirm"
		}
		switch {
		case readTools[tool.Name]:
			if tool.Annotations != readOnly || hasConfirm {
				t.Errorf("%s: read tool annotations %+v, confirm %v", tool.Name, tool.Annotations, hasConfirm)
			}
		case destructiveTools[tool.Name]:
			if tool.Annotations != destructive || !confirmRequired {
				t.Errorf("%s: destructive tool annotations %+v, confirm required %v", tool.Name, tool.Annotations, confirmRequired)
			}
		default:
			t.Errorf("%s is not in the test's lists", tool.Name)
		}
	}
}

func TestRateLimited(t *testing.T) {
	s := newTestServer(t, newFakeAPI())
	now := time.Unix(1_000_000, 0)
	s.limiter.now = func() time.Time { return now }
	for i := 0; i < callsPerMinute; i++ {
		if _, text, isError := callTool(t, s, "list_projects", map[string]any{}); isError {
			t.Fatalf("call %d refused: %s", i, text)
		}
	}
	if _, text, isError := callTool(t, s, "list_projects", map[string]any{}); !isError || !strings.Contains(text, "rate limited") {
		t.Errorf("call over the limit = %v %s", isError, text)
	}
	now = now.Add(time.Minute)
	if _, text, isError := callTool(t, s, "list_projects", map[string]any{}); isError {
		t.Errorf("call after the window refused: %s", text)
	}
}

func TestStagingAbsentIsNotAnError(t *testing.T) {
	api := newFakeAPI()
	api.staging = false
	s := newTestServer(t, api)
	out, text, isError := callTool(t, s, "get_staging", map[string]any{"project": "web"})
	if isError || out["has_staging"] != false {
		t.Errorf("no staging = %v %s", isError, text)
	}
	if _, text, isError := callTool(t, s, "get_staging", map[string]any{"project": "missing"}); !isError || !strings.Contains(text, "not found") {
		t.Errorf("missing project = %v %s", isError, text)
	}
}

func TestSSEParser(t *testing.T) {
	p := &sseParser{maxEvents: 10}
	// Split across writes, CRLF, comments, ids, multi-line data and no
	// final blank line.
	for _, chunk := range []string{": hb\n\nid: 1\nevent: lo", "g\r\ndata: {\"a\":\ndata: 1}\n\n", "event: x\ndata: tail"} {
		p.write([]byte(chunk))
	}
	p.finish()
	if len(p.events) != 2 || p.events[0].typ != "log" || string(p.events[0].data) != "{\"a\":\n1}" ||
		p.events[1].typ != "x" || string(p.events[1].data) != "tail" {
		t.Errorf("events = %+v", p.events)
	}
	// An event over maxEventBytes is dropped, the next one kept.
	p = &sseParser{maxEvents: 10}
	p.write([]byte("data: " + strings.Repeat("y", maxEventBytes+10) + "\n\ndata: ok\n\n"))
	if len(p.events) != 1 || string(p.events[0].data) != "ok" || !p.dropped {
		t.Errorf("oversized event: %d events, dropped %v", len(p.events), p.dropped)
	}
}

// listTools returns the names tools/list shows for api's token.
func listTools(t *testing.T, s http.Handler) []string {
	t.Helper()
	rep := send(t, s, modernBody("tools/list", nil), modernHeaders("tools/list", ""))
	var tools []Tool
	if err := json.Unmarshal(rep.msg.Result["tools"], &tools); err != nil {
		t.Fatalf("tools/list = %d %s", rep.status, rep.body)
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestDestructiveNeedsConfirm(t *testing.T) {
	api := deployFake()
	s := newTestServer(t, api)
	args := map[string]map[string]any{
		"deploy":   {"project": "web"},
		"restart":  {"project": "web"},
		"rollback": {"project": "web", "number": 2},
		"promote":  {"project": "web"},
	}
	for tool, base := range args {
		for _, confirm := range []any{nil, false, "true", 1} {
			a := map[string]any{}
			for k, v := range base {
				a[k] = v
			}
			if confirm != nil {
				a["confirm"] = confirm
			}
			if _, text, isError := callTool(t, s, tool, a); !isError || !strings.Contains(text, "confirm") {
				t.Errorf("%s with confirm %v = isError %v %s", tool, confirm, isError, text)
			}
		}
	}
	if posts := api.posts(); len(posts) != 0 {
		t.Fatalf("a write reached the API without confirm: %v", posts)
	}
	for _, a := range []map[string]any{{"project": "web"}, {"project": "web", "confirm": false}} {
		if _, text, _ := callTool(t, s, "deploy", a); !strings.Contains(text, "ask the user") {
			t.Errorf("deploy %v: the error does not say to ask the user: %s", a, text)
		}
	}
	for tool, base := range args {
		base["confirm"] = true
		if _, text, isError := callTool(t, s, tool, base); isError {
			t.Errorf("%s with confirm true: %s", tool, text)
		}
	}
	if got := len(api.posts()); got != 4 {
		t.Errorf("%d writes sent after confirm, want 4", got)
	}
}

func TestWriteToolsNeedADeployToken(t *testing.T) {
	// A read token sees the read tools only and gets a clear error.
	api := newFakeAPI()
	s := newTestServer(t, api)
	names := listTools(t, s)
	if len(names) != len(readTools) {
		t.Errorf("read token sees %v", names)
	}
	for _, n := range names {
		if !readTools[n] {
			t.Errorf("read token sees %s", n)
		}
	}
	for tool := range destructiveTools {
		a := map[string]any{"project": "web", "number": 2, "confirm": true}
		if tool != "rollback" && tool != "promote" {
			delete(a, "number")
		}
		_, text, isError := callTool(t, s, tool, a)
		if !isError || !strings.Contains(text, tool+" needs a token that can deploy") || !strings.Contains(text, "scope is read") {
			t.Errorf("%s with a read token = isError %v %s", tool, isError, text)
		}
	}
	// Scope comes before arguments: no confirm, still the scope error.
	if _, text, _ := callTool(t, s, "deploy", map[string]any{"project": "web"}); !strings.Contains(text, "needs a token that can deploy") {
		t.Errorf("read token without confirm = %s", text)
	}
	if _, text, _ := callTool(t, s, "create_backup", map[string]any{"project": "web", "confirm": true}); !strings.Contains(text, "needs a token that can deploy") {
		t.Errorf("create_backup with a read token = %s", text)
	}
	if posts := api.posts(); len(posts) != 0 {
		t.Fatalf("a read token's write reached the API: %v", posts)
	}

	// A write token whose owner is a viewer cannot deploy either, and is
	// told why.
	api.scope, api.role = "write", "viewer"
	if _, text, _ := callTool(t, s, "restart", map[string]any{"project": "web", "confirm": true}); !strings.Contains(text, "owner is a viewer") {
		t.Errorf("viewer's write token = %s", text)
	}
	// If the token cannot be looked up, only read tools are listed and a
	// write tool refuses.
	api.scope, api.role, api.canDeploy, api.tokenErr = "deploy", "member", true, true
	if names := listTools(t, s); len(names) != len(readTools) {
		t.Errorf("token lookup failed, tools/list = %v", names)
	}
	if _, text, isError := callTool(t, s, "deploy", map[string]any{"project": "web", "confirm": true}); !isError ||
		!strings.Contains(text, "could not check") {
		t.Errorf("token lookup failed, deploy = %s", text)
	}

	// A deploy token sees all twelve.
	api.tokenErr = false
	if names := listTools(t, s); len(names) != len(Tools()) {
		t.Errorf("deploy token sees %v", names)
	}
	if posts := api.posts(); len(posts) != 0 {
		t.Fatalf("writes sent: %v", posts)
	}
}

// TestInstructionsNameTheTokenScope: the handshake tells the model what its
// token can do, in both eras, so a read token's agent says it cannot deploy
// instead of looking for a deploy tool.
func TestInstructionsNameTheTokenScope(t *testing.T) {
	api := newFakeAPI()
	s := newTestServer(t, api)
	get := func() (legacy, modern string) {
		t.Helper()
		rep := send(t, s, legacyBody("initialize", map[string]any{
			"protocolVersion": ProtocolLegacy, "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "test", "version": "1"},
		}), nil)
		if err := json.Unmarshal(rep.msg.Result["instructions"], &legacy); err != nil {
			t.Fatalf("initialize: %s", rep.body)
		}
		rep = send(t, s, modernBody("server/discover", nil), modernHeaders("server/discover", ""))
		if err := json.Unmarshal(rep.msg.Result["instructions"], &modern); err != nil {
			t.Fatalf("server/discover: %s", rep.body)
		}
		return legacy, modern
	}
	for _, c := range []struct {
		name, scope, role string
		canDeploy, err    bool
		want, not         string
	}{
		{"read", "read", "member", false, false, "This token's scope is read: it cannot deploy", "it can deploy"},
		{"viewer's write", "write", "viewer", false, false, "scope is write, but its owner is a viewer: it cannot deploy", "it can deploy"},
		{"deploy", "deploy", "member", true, false, "This token's scope is deploy: it can deploy", "cannot"},
		{"lookup failed", "deploy", "member", true, true, "", "This token's scope"},
	} {
		api.scope, api.role, api.canDeploy, api.tokenErr = c.scope, c.role, c.canDeploy, c.err
		legacy, modern := get()
		for era, text := range map[string]string{"initialize": legacy, "server/discover": modern} {
			if !strings.HasPrefix(text, instructions) {
				t.Errorf("%s token, %s: instructions lost the base text: %q", c.name, era, text)
			}
			if c.want != "" && !strings.Contains(text, c.want) {
				t.Errorf("%s token, %s: %q, want it to contain %q", c.name, era, text, c.want)
			}
			if strings.Contains(text, c.not) {
				t.Errorf("%s token, %s: %q contains %q", c.name, era, text, c.not)
			}
		}
	}
}

type auditCall struct{ tool, project string }

func auditingServer(t *testing.T, api *fakeAPI) (*Server, func() []auditCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []auditCall
	s := New(Config{Version: "test", EnvValues: envValues, Audit: func(r *http.Request, tool, project string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, auditCall{tool, project})
	}})
	s.SetAPI(api)
	return s, func() []auditCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]auditCall(nil), calls...)
	}
}

func TestWritesAreAudited(t *testing.T) {
	api := deployFake()
	s, audited := auditingServer(t, api)

	out, text, isError := callTool(t, s, "deploy", map[string]any{"project": "web", "confirm": true})
	if isError || !strings.Contains(text, "get_deployment with project web and number 3") {
		t.Errorf("deploy = %s", text)
	}
	if d, _ := out["deployment"].(map[string]any); d["number"] != float64(3) {
		t.Errorf("deploy deployment = %v", out["deployment"])
	}
	// Promote by the live project's name: the call goes to its staging and
	// the audit line names live, as the REST route's does.
	if _, text, isError := callTool(t, s, "promote", map[string]any{"project": "web", "confirm": true}); isError {
		t.Errorf("promote = %s", text)
	}
	if _, text, isError := callTool(t, s, "promote", map[string]any{"project": "web", "number": 4, "confirm": true}); isError {
		t.Errorf("promote 4 = %s", text)
	}
	if _, text, isError := callTool(t, s, "create_backup", map[string]any{"project": "web", "destination": "s3", "confirm": true}); isError {
		t.Errorf("create_backup = %s", text)
	}
	if !strings.Contains(api.backupBody, `"destination":"s3"`) {
		t.Errorf("backup body = %q", api.backupBody)
	}
	want := []auditCall{{"deploy", "web"}, {"promote", "web"}, {"promote", "web"}, {"create_backup", "web"}}
	if got := audited(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("audited %v, want %v", got, want)
	}
	posts := api.posts()
	if len(posts) != 4 || !strings.HasSuffix(posts[1], "/web-staging/deployments/5/promote") ||
		!strings.HasSuffix(posts[2], "/web-staging/deployments/4/promote") {
		t.Errorf("writes = %v", posts)
	}

	// Refused by the router, or failed: an error, scrubbed, and no line.
	api.refuseWrites = true
	if _, text, isError := callTool(t, s, "deploy", map[string]any{"project": "web", "confirm": true}); !isError ||
		!strings.Contains(text, "not allowed") {
		t.Errorf("refused deploy = %s", text)
	}
	api.refuseWrites, api.restartFails, api.backupFails = false, true, true
	_, text, isError = callTool(t, s, "restart", map[string]any{"project": "web", "confirm": true})
	if !isError || strings.Contains(text, plantedSecret) || !strings.Contains(text, "bad value [redacted]") {
		t.Errorf("failed restart = %s", text)
	}
	_, text, isError = callTool(t, s, "create_backup", map[string]any{"project": "web", "confirm": true})
	if !isError || strings.Contains(text, plantedSecret) || !strings.Contains(text, "backup of web failed: pg_dump: [redacted]") {
		t.Errorf("failed backup = %s", text)
	}
	if got := audited(); len(got) != len(want) {
		t.Errorf("a refused or failed write was audited: %v", got[len(want):])
	}

	// No staging: say so without sending anything; a typo is not found.
	api.staging = false
	before := len(api.posts())
	if _, text, isError := callTool(t, s, "promote", map[string]any{"project": "web", "confirm": true}); !isError ||
		!strings.Contains(text, "has no staging") {
		t.Errorf("promote without staging = %s", text)
	}
	if _, text, isError := callTool(t, s, "promote", map[string]any{"project": "missing", "confirm": true}); !isError ||
		!strings.Contains(text, "not found") {
		t.Errorf("promote missing = %s", text)
	}
	if len(api.posts()) != before {
		t.Errorf("promote without staging sent a write")
	}
	if _, text, _ := callTool(t, s, "create_backup", map[string]any{"project": "web", "destination": "ftp"}); !strings.Contains(text, "destination") {
		t.Errorf("bad destination = %s", text)
	}
}

func TestWriteOutlivesTheClient(t *testing.T) {
	api := deployFake()
	api.restartDelay = 300 * time.Millisecond
	api.restarts = make(chan bool, 2)
	s, audited := auditingServer(t, api)

	// The client hangs up 30 ms in; the restart must still finish.
	ctx, cancel := context.WithCancel(context.Background())
	body := modernBody("tools/call", map[string]any{"name": "restart", "arguments": map[string]any{"project": "web", "confirm": true}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+testToken)
	for k, v := range modernHeaders("tools/call", "restart") {
		req.Header.Set(k, v)
	}
	time.AfterFunc(30*time.Millisecond, cancel)
	returned := make(chan struct{})
	go func() {
		s.ServeHTTP(httptest.NewRecorder(), req)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("the tool call did not return when the client hung up")
	}
	select {
	case cancelled := <-api.restarts:
		if cancelled {
			t.Fatal("the restart was cancelled with the client")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the restart never finished")
	}
	deadline := time.Now().Add(time.Second)
	for len(audited()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := audited(); len(got) != 1 || got[0] != (auditCall{"restart", "web"}) {
		t.Errorf("audited %v after the client left", got)
	}

	// Past its wait, a tool says the work is still running.
	old := restartWait
	restartWait = 20 * time.Millisecond
	defer func() { restartWait = old }()
	out, text, isError := callTool(t, s, "restart", map[string]any{"project": "web", "confirm": true})
	if isError || out["status"] != stillRunning || !strings.Contains(text, "get_project") {
		t.Errorf("slow restart = %s", text)
	}
	if cancelled := <-api.restarts; cancelled {
		t.Error("the slow restart was cancelled")
	}
}

func TestNewestRecentManual(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	b := func(id, kind, status string, ago time.Duration) object {
		return object{"id": json.RawMessage(id), "kind": json.RawMessage(`"` + kind + `"`),
			"status":     json.RawMessage(`"` + status + `"`),
			"created_at": json.RawMessage(`"` + now.Add(-ago).Format("2006-01-02T15:04:05.000Z") + `"`)}
	}
	for name, c := range map[string]struct {
		list []object
		want string
	}{
		"none":              {nil, ""},
		"old manual":        {[]object{b("1", "manual", "done", 11*time.Minute)}, ""},
		"failed manual":     {[]object{b("1", "manual", "failed", time.Minute)}, ""},
		"recent scheduled":  {[]object{b("1", "scheduled", "done", time.Minute)}, ""},
		"recent manual":     {[]object{b("1", "manual", "done", 9*time.Minute)}, "1"},
		"pending manual":    {[]object{b("1", "manual", "pending", 0)}, "1"},
		"newest of several": {[]object{b("1", "manual", "done", 8*time.Minute), b("2", "manual", "done", 2*time.Minute)}, "2"},
	} {
		got := newestRecentManual(c.list, now)
		if id := string(got["id"]); id != c.want {
			t.Errorf("%s: got id %q, want %q", name, id, c.want)
		}
	}
}

// Calls at once still take one backup: the gap check and the POST are one
// step per project.
func TestParallelBackupsTakeOne(t *testing.T) {
	api := deployFake()
	api.madeBackups = &[]map[string]any{}
	s := newTestServer(t, api)
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			callTool(t, s, "create_backup", map[string]any{"project": "web", "confirm": true})
		}()
	}
	wg.Wait()
	api.mu.Lock()
	defer api.mu.Unlock()
	if n := len(*api.madeBackups); n != 1 {
		t.Errorf("%d backups taken, want 1", n)
	}
}
