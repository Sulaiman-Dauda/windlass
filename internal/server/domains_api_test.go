package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

func TestDomainCRUD(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")

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
