package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

// The create response carries the same fields as an item of the list route,
// so a client can decode both into one type.
func TestCreateDomainResponseShape(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Image: "example/app", ContainerPorts: []int{3000}},
	}}

	e.agent.Containers = []agent.Container{{
		ID: "c1", Name: "app-web-1", State: "running",
		ComposeProject: "app", ComposeService: "web", IPAddress: "172.18.0.5",
	}}

	rec := e.do(t, http.MethodPost, "/api/v1/projects/app/domains", map[string]any{
		"hostname": "app.example.com", "service": "web", "container_port": 3000,
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create domain = %d: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Hostname      string `json:"hostname"`
		Service       string `json:"service"`
		ContainerPort int64  `json:"container_port"`
		Status        string `json:"status"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode 201 body: %v", err)
	}
	if got.Hostname != "app.example.com" || got.Service != "web" || got.ContainerPort != 3000 {
		t.Errorf("201 body = %+v, want hostname, service and container_port", got)
	}
	if got.Status == "" {
		t.Errorf("201 body has no status: %+v", got)
	}
}

func TestDomainCRUD(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Image: "example/app", ContainerPorts: []int{3000}},
	}}

	// A running container backs the domain's service.
	e.agent.Containers = []agent.Container{{
		ID: "c1", Name: "app-web-1", State: "running",
		ComposeProject: "app", ComposeService: "web", IPAddress: "172.18.0.5",
	}}

	// Create.
	rec := e.do(t, http.MethodPost, "/api/v1/projects/app/domains", map[string]any{
		"hostname": "App.Example.COM", "service": "web", "container_port": 3000,
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create domain = %d: %s", rec.Code, rec.Body.String())
	}
	// Hostname normalized to lowercase.
	if !strings.Contains(rec.Body.String(), `"app.example.com"`) {
		t.Errorf("hostname not normalized: %s", rec.Body.String())
	}

	// Duplicate conflicts.
	rec = e.do(t, http.MethodPost, "/api/v1/projects/app/domains", map[string]any{
		"hostname": "app.example.com", "service": "web", "container_port": 3000,
	}, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d, want 409", rec.Code)
	}

	// Invalid hostnames rejected.
	for _, bad := range []string{"nodots", "-bad.example.com", "a b.example.com", ""} {
		rec = e.do(t, http.MethodPost, "/api/v1/projects/app/domains", map[string]any{
			"hostname": bad, "service": "web", "container_port": 3000,
		}, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("hostname %q = %d, want 400", bad, rec.Code)
		}
	}
	for _, bad := range []map[string]any{
		{"hostname": "bad-service.example.com", "service": "missing", "container_port": 3000},
		{"hostname": "bad-port.example.com", "service": "web", "container_port": 8080},
	} {
		rec = e.do(t, http.MethodPost, "/api/v1/projects/app/domains", bad, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("invalid domain target %#v = %d: %s", bad, rec.Code, rec.Body.String())
		}
	}

	// List reports status.
	rec = e.do(t, http.MethodGet, "/api/v1/projects/app/domains", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app.example.com") {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}

	// Proxy status endpoint.
	rec = e.do(t, http.MethodGet, "/api/v1/proxy/status", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"available":true`) {
		t.Fatalf("proxy status = %d: %s", rec.Code, rec.Body.String())
	}

	// Delete.
	rec = e.do(t, http.MethodDelete, "/api/v1/projects/app/domains/app.example.com", nil, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	rec = e.do(t, http.MethodDelete, "/api/v1/projects/app/domains/app.example.com", nil, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete again = %d, want 404", rec.Code)
	}
}

func TestDomainSyncBuildsRoutes(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Image: "example/app", ContainerPorts: []int{3000}},
	}}

	e.agent.Containers = []agent.Container{{
		ID: "c1", Name: "app-web-1", State: "running",
		ComposeProject: "app", ComposeService: "web", IPAddress: "172.18.0.5",
	}}

	rec := e.do(t, http.MethodPost, "/api/v1/projects/app/domains", map[string]any{
		"hostname": "app.example.com", "service": "web", "container_port": 3000,
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}

	// Drive a sync directly (the background Run loop is not started in tests).
	if err := e.api.Proxy.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(e.agent.Routes) != 1 {
		t.Fatalf("routes applied = %d, want 1", len(e.agent.Routes))
	}
	r := e.agent.Routes[0]
	if r.Hostname != "app.example.com" || r.Upstream != "172.18.0.5:3000" || r.ID != "windlass_route_app.example.com" {
		t.Errorf("route = %+v", r)
	}

	// Stopped container → domain has no upstream → no route (but no error).
	e.agent.Containers[0].State = "exited"
	if err := e.api.Proxy.Sync(context.Background()); err != nil {
		t.Fatalf("Sync with dead container: %v", err)
	}
	if len(e.agent.Routes) != 0 {
		t.Errorf("routes = %+v, want none for stopped container", e.agent.Routes)
	}
}

// A project that could take the panel's hostname would be handed the panel's
// sign-ins, so each side refuses a name the other holds.
func TestPanelAndProjectsNeverShareAHostname(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Image: "example/app", ContainerPorts: []int{3000}},
	}}

	rec := e.do(t, http.MethodPut, "/api/v1/system/panel-domain", map[string]any{"hostname": "panel.example.com"}, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("set panel domain = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodPost, "/api/v1/projects/app/domains", map[string]any{
		"hostname": "Panel.Example.com", "service": "web", "container_port": 3000,
	}, cookie)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "panel's domain") {
		t.Fatalf("panel hostname as a project domain = %d: %s, want 409 naming the panel", rec.Code, rec.Body.String())
	}

	rec = e.do(t, http.MethodPost, "/api/v1/projects/app/domains", map[string]any{
		"hostname": "app.example.com", "service": "web", "container_port": 3000,
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create domain = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodPut, "/api/v1/system/panel-domain", map[string]any{"hostname": "APP.example.com"}, cookie)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "routes to app") {
		t.Fatalf("project hostname as the panel domain = %d: %s, want 409 naming the project", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, "/api/v1/system/panel-domain", nil, cookie)
	if !strings.Contains(rec.Body.String(), `"panel.example.com"`) {
		t.Fatalf("a refused panel domain replaced the saved one: %s", rec.Body.String())
	}
	if e.agent.PanelDomain != "panel.example.com" {
		t.Fatalf("Caddy was given %q as the panel hostname", e.agent.PanelDomain)
	}
}

// The file editor accepts .windlass.json, and listing projects indexes it, so
// a member can write domain rows that never pass the domain API's checks.
// Nothing written that way may be routed over the panel's hostname.
func TestManifestCannotPutAProjectOnThePanelHostname(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.Containers = []agent.Container{{ComposeProject: "app", ComposeService: "web", State: "running", IPAddress: "10.0.1.2"}}

	rec := e.do(t, http.MethodPut, "/api/v1/system/panel-domain", map[string]any{"hostname": "panel.example.com"}, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("set panel domain = %d: %s", rec.Code, rec.Body.String())
	}
	manifest := `{"version":1,"domains":[` +
		`{"hostname":"Panel.Example.com","service":"web","container_port":3000},` +
		`{"hostname":"*.example.com","service":"web","container_port":3000},` +
		`{"hostname":"app.example.com","service":"web","container_port":3000}]}`
	rec = e.do(t, http.MethodPut, "/api/v1/projects/app/files/.windlass.json", map[string]string{"content": manifest}, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("write manifest = %d: %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, http.MethodGet, "/api/v1/projects", nil, cookie); rec.Code != http.StatusOK {
		t.Fatalf("list projects = %d: %s", rec.Code, rec.Body.String())
	}
	if err := e.api.Proxy.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(e.agent.Routes) != 1 || e.agent.Routes[0].Hostname != "app.example.com" {
		t.Fatalf("routes = %+v, want only app.example.com", e.agent.Routes)
	}
}

// A manifest is a file, and the last one indexed used to take any hostname it
// named, traffic included. A name stays with the project whose manifest still
// lists it, in whatever case the second manifest writes it.
func TestManifestCannotTakeAnotherProjectsHostname(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.prepareDeployableProject(t, cookie, "other")
	services := agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Image: "example/app", ContainerPorts: []int{3000}},
	}}
	e.agent.Resolved["app"], e.agent.Resolved["other"] = services, services
	e.agent.Containers = []agent.Container{
		{ComposeProject: "app", ComposeService: "web", State: "running", IPAddress: "10.0.1.2"},
		{ComposeProject: "other", ComposeService: "web", State: "running", IPAddress: "10.0.1.3"},
	}
	rec := e.do(t, http.MethodPost, "/api/v1/projects/app/domains", map[string]any{
		"hostname": "app.example.com", "service": "web", "container_port": 3000,
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create domain = %d: %s", rec.Code, rec.Body.String())
	}

	write := func(project, domains string) {
		t.Helper()
		body := map[string]string{"content": `{"version":1,"source":"manual","domains":[` + domains + `]}`}
		if rec := e.do(t, http.MethodPut, "/api/v1/projects/"+project+"/files/.windlass.json", body, cookie); rec.Code != http.StatusNoContent {
			t.Fatalf("write %s manifest = %d: %s", project, rec.Code, rec.Body.String())
		}
		if rec := e.do(t, http.MethodGet, "/api/v1/projects", nil, cookie); rec.Code != http.StatusOK {
			t.Fatalf("list projects = %d: %s", rec.Code, rec.Body.String())
		}
		if err := e.api.Proxy.Sync(context.Background()); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}
	upstreams := func() string {
		out := ""
		for _, r := range e.agent.Routes {
			out += r.Hostname + "=" + r.Upstream + " "
		}
		return out
	}

	for _, claim := range []string{"app.example.com", "App.Example.COM"} {
		write("other", `{"hostname":"`+claim+`","service":"web","container_port":3000}`)
		if got := upstreams(); got != "app.example.com=10.0.1.2:3000 " {
			t.Fatalf("after other's manifest claimed %q, routes = %q, want app to keep its hostname", claim, got)
		}
	}

	// The holder lets go, as a renamed project's old directory does, and the
	// claim is then honoured, stored in lower case.
	write("app", "")
	if got := upstreams(); got != "app.example.com=10.0.1.3:3000 " {
		t.Fatalf("after app gave the hostname up, routes = %q, want it on other", got)
	}

	// Now other holds it and app is indexed first. While other's manifest
	// lists the name app is refused; once it does not, app takes the name in
	// the same pass, before other's own row has been cleared.
	claim := `{"hostname":"app.example.com","service":"web","container_port":3000}`
	write("app", claim)
	if got := upstreams(); got != "app.example.com=10.0.1.3:3000 " {
		t.Fatalf("app's manifest took the hostname back from other: %q", got)
	}
	empty := map[string]string{"content": `{"version":1,"source":"manual","domains":[]}`}
	if rec := e.do(t, http.MethodPut, "/api/v1/projects/other/files/.windlass.json", empty, cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("write other manifest = %d: %s", rec.Code, rec.Body.String())
	}
	write("app", claim)
	if got := upstreams(); got != "app.example.com=10.0.1.2:3000 " {
		t.Fatalf("after other gave the hostname up, routes = %q, want it on app", got)
	}
}
