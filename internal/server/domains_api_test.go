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
