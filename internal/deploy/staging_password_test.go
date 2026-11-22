package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/projects"
)

// A password-protected staging's health check goes to the container, so
// the password on its domain does not fail the deploy.
func TestProtectedStagingHealthCheckDialsContainer(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.deploy.projects.Create(ctx, projects.CreateReq{Name: "site"}); err != nil {
		t.Fatal(err)
	}
	protect, err := projects.NewStagingProtect("a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.deploy.projects.CreateStagingCopy(ctx, projects.CopyReq{Live: "site", Protect: protect,
		Domains: []projects.DomainConfig{{Hostname: "staging-site.example.com", Service: "web", ContainerPort: 8080}},
	}); err != nil {
		t.Fatal(err)
	}
	check := agent.ApplicationHealthCheck{Service: "web", URL: "https://staging-site.example.com/health", ExpectedStatus: 200}
	other := agent.ApplicationHealthCheck{Service: "web", URL: "https://elsewhere.example.com/up", ExpectedStatus: 200}
	e.agent.Resolved["site-staging"] = agent.ResolvedConfig{
		Services:     map[string]agent.ResolvedService{"web": {Image: "nginx:alpine"}},
		HealthChecks: []agent.ApplicationHealthCheck{check, other},
	}
	e.agent.Statuses["site-staging"] = []agent.ServiceStatus{{Service: "web", Name: "site-staging-web-1", State: "running"}}
	e.agent.Containers = []agent.Container{{ID: "c1", Name: "site-staging-web-1", State: "running",
		ComposeProject: "site-staging", ComposeService: "web", IPAddress: "172.18.0.9"}}
	d, err := e.deploy.Deploy(ctx, "site-staging", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if final := e.runUntilFinished(t, d.ID); final.Status != "succeeded" {
		t.Fatalf("protected staging deploy = %s: %s", final.Status, final.Error.String)
	}
	calls := strings.Join(e.agent.Calls, " | ")
	if !strings.Contains(calls, "host.httpcheck(https://staging-site.example.com/health via 172.18.0.9:8080)") {
		t.Fatalf("check did not dial the container: %s", calls)
	}
	if !strings.Contains(calls, "host.httpcheck(https://elsewhere.example.com/up)") {
		t.Errorf("a URL on another host was redirected: %s", calls)
	}
	// Windlass in a container cannot reach the container; the agent falls
	// back to the URL and the staging password answers. That passes.
	e.agent.HTTPResponses[check.URL] = agent.HTTPCheckResult{StatusCode: 401, BehindPassword: true}
	d, err = e.deploy.Deploy(ctx, "site-staging", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if final := e.runUntilFinished(t, d.ID); final.Status != "succeeded" {
		t.Fatalf("deploy behind the password = %s: %s", final.Status, final.Error.String)
	}
	delete(e.agent.HTTPResponses, check.URL)

	// Without the password it goes through Caddy as before.
	if err := e.deploy.projects.SetStagingProtect(ctx, "site-staging", nil); err != nil {
		t.Fatal(err)
	}
	e.agent.Calls = nil
	d, err = e.deploy.Deploy(ctx, "site-staging", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if final := e.runUntilFinished(t, d.ID); final.Status != "succeeded" {
		t.Fatalf("open staging deploy = %s: %s", final.Status, final.Error.String)
	}
	if calls := strings.Join(e.agent.Calls, " | "); strings.Contains(calls, " via ") {
		t.Errorf("open staging still dials the container: %s", calls)
	}
}
