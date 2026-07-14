package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/windlass-dev/windlass/internal/agent"
)

func TestTemplateCreatesAndDeploys(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)

	// Templates list.
	rec := e.do(t, http.MethodGet, "/api/v1/templates", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "postgres") {
		t.Fatalf("templates = %d: %s", rec.Code, rec.Body.String())
	}

	// The deployment verify step needs a healthy status for the new project.
	e.agent.Statuses["mydb"] = []agent.ServiceStatus{{Service: "postgres", State: "running", Health: "healthy"}}
	e.agent.Resolved["mydb"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"postgres": {Image: "postgres:17-alpine"},
	}}

	rec = e.do(t, http.MethodPost, "/api/v1/templates/postgres", map[string]string{"name": "mydb"}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create from template = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "mydb", "1", "succeeded")

	// The compose file is a plain project file on disk.
	data, err := e.agent.FS().ReadFile(context.Background(), "mydb", "compose.yaml")
	if err != nil || !strings.Contains(string(data), "postgres:17-alpine") {
		t.Fatalf("compose = %q, %v", data, err)
	}

	// Credentials were generated into the env, encrypted.
	rec = e.do(t, http.MethodGet, "/api/v1/projects/mydb/env", nil, cookie)
	var env map[string]string
	json.NewDecoder(rec.Body).Decode(&env)
	if env["POSTGRES_PASSWORD"] == "" || env["POSTGRES_DB"] != "mydb" {
		t.Errorf("env = %v", env)
	}

	// Unknown template 400s.
	rec = e.do(t, http.MethodPost, "/api/v1/templates/oracle", map[string]string{}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown template = %d", rec.Code)
	}
}

func TestAppTemplateAttachesDomainAndDeploys(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)

	// The deploy verify step and the domain's port check both read this.
	e.agent.Statuses["status"] = []agent.ServiceStatus{{Service: "uptime-kuma", State: "running", Health: "healthy"}}
	e.agent.Resolved["status"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"uptime-kuma": {Image: "louislam/uptime-kuma:1", ContainerPorts: []int{3001}},
	}}

	// An app template without a domain is rejected.
	rec := e.do(t, http.MethodPost, "/api/v1/templates/uptime-kuma",
		map[string]any{"name": "status"}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing domain = %d, want 400: %s", rec.Code, rec.Body.String())
	}

	// With a domain it creates, attaches the route, and deploys.
	rec = e.do(t, http.MethodPost, "/api/v1/templates/uptime-kuma",
		map[string]any{"name": "status", "domain": "status.example.com"}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create app template = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "status", "1", "succeeded")

	// The domain is now attached to the project.
	rec = e.do(t, http.MethodGet, "/api/v1/projects/status/domains", nil, cookie)
	if !strings.Contains(rec.Body.String(), "status.example.com") {
		t.Errorf("domain not attached: %s", rec.Body.String())
	}
}

func TestContainerLogsSSE(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Containers = []agent.Container{{
		ID: "c1", Name: "app-web-1", State: "running",
		ComposeProject: "app", ComposeService: "web",
	}}

	srv := httptest.NewServer(e.handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/api/v1/projects/app/logs?service=web&follow=false", nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var sawLine bool
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.Contains(sc.Text(), "log line from c1") {
			sawLine = true
		}
	}
	if !sawLine {
		t.Error("no log lines streamed")
	}
}

func TestTerminalWebSocketEcho(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Containers = []agent.Container{{
		ID: "c1", Name: "app-web-1", State: "running",
		ComposeProject: "app", ComposeService: "web",
	}}

	srv := httptest.NewServer(e.handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/projects/app/terminal?service=web"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{cookie.Name + "=" + cookie.Value}},
	})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.CloseNow()

	// The fake exec session echoes input back as output.
	msg, _ := json.Marshal(map[string]string{"type": "input", "data": "whoami\n"})
	if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageBinary || string(data) != "whoami\n" {
		t.Errorf("echo = %v %q", typ, data)
	}

	// Resize is accepted without killing the session.
	resize, _ := json.Marshal(map[string]any{"type": "resize", "cols": 80, "rows": 24})
	if err := conn.Write(ctx, websocket.MessageText, resize); err != nil {
		t.Fatalf("resize write: %v", err)
	}
}

func TestSystemMetrics(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.agent.Metrics = agent.HostMetrics{MemoryTotal: 2 << 30, MemoryUsed: 1 << 30, CPUPercent: 12.5}
	e.agent.Containers = []agent.Container{
		{ID: "a", State: "running"}, {ID: "b", State: "exited"},
	}

	rec := e.do(t, http.MethodGet, "/api/v1/system/metrics", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"cpu_percent":12.5`, `"running":1`, `"total":2`, `"hostname":"fake-node"`} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s in %s", want, body)
		}
	}
}

func TestDockerImageLifecycle(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.agent.ImageUsage = agent.ImageDiskUsage{TotalCount: 12, ActiveCount: 3,
		TotalBytes: 2 << 30, ReclaimableBytes: 1 << 30}
	e.agent.PruneResult = agent.ImagePruneResult{Deleted: 4, ReclaimedBytes: 512 << 20}

	rec := e.do(t, http.MethodGet, "/api/v1/system/docker/images", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"reclaimable_bytes":1073741824`) {
		t.Fatalf("image usage = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodPost, "/api/v1/system/docker/images/prune", map[string]int{
		"retention_days": 7, "keep_deployments": 5,
	}, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":4`) {
		t.Fatalf("image prune = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPanelDomainSettings(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	rec := e.do(t, http.MethodGet, "/api/v1/system/panel-domain", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"configured":false`) {
		t.Fatalf("initial panel domain = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodPut, "/api/v1/system/panel-domain", map[string]string{
		"hostname": "not a hostname",
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid hostname = %d", rec.Code)
	}
	rec = e.do(t, http.MethodPut, "/api/v1/system/panel-domain", map[string]string{
		"hostname": "Windlass.Example.COM",
	}, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"url":"https://windlass.example.com"`) {
		t.Fatalf("set panel domain = %d: %s", rec.Code, rec.Body.String())
	}
	if e.agent.PanelDomain != "windlass.example.com" {
		t.Fatalf("agent panel domain = %q", e.agent.PanelDomain)
	}
	rec = e.do(t, http.MethodPut, "/api/v1/system/panel-domain", map[string]string{"hostname": ""}, cookie)
	if rec.Code != http.StatusOK || e.agent.PanelDomain != "" {
		t.Fatalf("remove panel domain = %d, agent=%q", rec.Code, e.agent.PanelDomain)
	}
}
