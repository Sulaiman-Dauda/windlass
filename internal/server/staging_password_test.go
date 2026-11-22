package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/windlass-dev/windlass/internal/agent"
)

// stagingRoute syncs the proxy and returns staging's route, nil when it is
// not routed.
func stagingRoute(t *testing.T, e *testEnv) *agent.Route {
	t.Helper()
	e.agent.Containers = []agent.Container{
		{ID: "c1", Name: "app-web-1", State: "running", ComposeProject: "app", ComposeService: "web", IPAddress: "172.18.0.5"},
		{ID: "c2", Name: "app-staging-web-1", State: "running", ComposeProject: "app-staging", ComposeService: "web", IPAddress: "172.18.0.6"},
	}
	if err := e.api.Proxy.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, r := range e.agent.Routes {
		if r.Hostname == "staging-app.example.com" {
			return &r
		}
	}
	return nil
}

func TestStagingPasswordOnByDefault(t *testing.T) {
	e := newTestEnv(t)
	withMCP(t, e)
	cookie := e.login(t)
	prepareStagingLive(t, e, cookie)

	rec := e.do(t, http.MethodPost, "/api/v1/projects/app/staging", map[string]any{"acknowledged": true}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	created := decode[struct {
		Result struct {
			Password     string `json:"password"`
			PasswordUser string `json:"password_user"`
		}
	}](t, rec.Body.String())
	if len(created.Result.Password) != 20 || created.Result.PasswordUser != "staging" {
		t.Fatalf("created = %s", rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app-staging", "1", "succeeded")

	manifest := string(e.agent.Files["app-staging"][".windlass.json"])
	if strings.Contains(manifest, created.Result.Password) {
		t.Fatal("the password itself is in .windlass.json")
	}
	route := stagingRoute(t, e)
	if route == nil || route.BasicAuth == nil || route.BasicAuth.User != "staging" {
		t.Fatalf("staging route = %+v", route)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(route.BasicAuth.Hash), []byte(created.Result.Password)); err != nil {
		t.Fatalf("route hash does not match the shown password: %v", err)
	}
	for _, r := range e.agent.Routes {
		if r.Hostname != "staging-app.example.com" && r.BasicAuth != nil {
			t.Errorf("live route %s got a password", r.Hostname)
		}
	}

	// Status and MCP say it is on; neither returns the hash.
	rec = e.do(t, http.MethodGet, "/api/v1/projects/app/staging", nil, cookie)
	if !strings.Contains(rec.Body.String(), `"password_protected":true`) || strings.Contains(rec.Body.String(), "$2a$") {
		t.Errorf("status = %s", rec.Body.String())
	}
	read := e.createToken(t, cookie, map[string]any{"name": "agent", "scope": "read"})
	if s := e.mcpCall(t, read.Token, "get_staging", map[string]any{"project": "app"}); !strings.Contains(s.text, `"password_protected":true`) ||
		strings.Contains(s.text, "$2a$") {
		t.Errorf("get_staging = %s", s.body)
	}

	// The hash is masked for accounts without the secrets flag, and a
	// masked file cannot be written back.
	viewer := e.viewerCookie(t, cookie)
	rec = e.do(t, http.MethodGet, "/api/v1/projects/app-staging/files/.windlass.json", nil, viewer)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "$2a$") || !strings.Contains(rec.Body.String(), "********") {
		t.Errorf("viewer read = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.doBearer(t, http.MethodGet, "/api/v1/projects/app-staging/files/.windlass.json", read.Token, nil); strings.Contains(rec.Body.String(), "$2a$") {
		t.Errorf("token read = %s", rec.Body.String())
	}
	member := e.createToken(t, cookie, map[string]any{"name": "ci", "scope": "write"})
	masked := e.doBearer(t, http.MethodGet, "/api/v1/projects/app-staging/files/.windlass.json", member.Token, nil)
	content := decode[struct{ Content string }](t, masked.Body.String()).Content
	if rec := e.doBearer(t, http.MethodPut, "/api/v1/projects/app-staging/files/.windlass.json", member.Token,
		map[string]string{"content": content}); rec.Code != http.StatusBadRequest {
		t.Errorf("masked write = %d: %s", rec.Code, rec.Body.String())
	}

	// Nor can a token drop it by writing the manifest without it.
	stripped := strings.Replace(manifest, `"protect"`, `"protect_gone"`, 1)
	if rec := e.doBearer(t, http.MethodPut, "/api/v1/projects/app-staging/files/.windlass.json", member.Token,
		map[string]string{"content": stripped}); rec.Code != http.StatusForbidden {
		t.Errorf("token manifest write without protect = %d: %s", rec.Code, rec.Body.String())
	}
	if route := stagingRoute(t, e); route == nil || route.BasicAuth == nil {
		t.Fatalf("route after token manifest write = %+v", route)
	}

	// Tokens cannot set it; a person can, and can turn it off.
	if rec := e.doBearer(t, http.MethodPut, "/api/v1/projects/app-staging/staging/password", member.Token,
		map[string]any{"enabled": false}); rec.Code != http.StatusForbidden {
		t.Errorf("token set password = %d", rec.Code)
	}
	if rec := e.do(t, http.MethodPut, "/api/v1/projects/app-staging/staging/password",
		map[string]any{"enabled": true, "password": "short"}, cookie); rec.Code != http.StatusBadRequest {
		t.Errorf("short password = %d", rec.Code)
	}
	rec = e.do(t, http.MethodPut, "/api/v1/projects/app-staging/staging/password",
		map[string]any{"enabled": true, "password": "correct horse battery"}, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"password":"correct horse battery"`) {
		t.Fatalf("set password = %d: %s", rec.Code, rec.Body.String())
	}
	route = stagingRoute(t, e)
	if route == nil || route.BasicAuth == nil ||
		bcrypt.CompareHashAndPassword([]byte(route.BasicAuth.Hash), []byte("correct horse battery")) != nil {
		t.Fatalf("route after new password = %+v", route)
	}
	if rec := e.do(t, http.MethodPut, "/api/v1/projects/app-staging/staging/password",
		map[string]any{"enabled": false}, cookie); rec.Code != http.StatusOK {
		t.Fatalf("turn off = %d: %s", rec.Code, rec.Body.String())
	}
	if route = stagingRoute(t, e); route == nil || route.BasicAuth != nil {
		t.Errorf("route after turning off = %+v", route)
	}
	var audits int
	if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'project.staging_password'
		AND detail NOT LIKE '%horse%'`).Scan(&audits); err != nil || audits != 2 {
		t.Errorf("audit rows = %d, %v", audits, err)
	}
	if rec := e.do(t, http.MethodPut, "/api/v1/projects/app/staging/password", map[string]any{"enabled": false}, cookie); rec.Code != http.StatusNotFound {
		t.Errorf("password on live = %d", rec.Code)
	}
}

func TestStagingWithoutPasswordAndBadHash(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	prepareStagingLive(t, e, cookie)
	rec := e.do(t, http.MethodPost, "/api/v1/projects/app/staging",
		map[string]any{"acknowledged": true, "no_password": true}, cookie)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), `"password"`) {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app-staging", "1", "succeeded")
	if route := stagingRoute(t, e); route == nil || route.BasicAuth != nil {
		t.Fatalf("open staging route = %+v", route)
	}

	// A hand-edited hash Caddy would refuse keeps staging off the proxy,
	// never open, and leaves live's route alone.
	manifest := string(e.agent.Files["app-staging"][".windlass.json"])
	manifest = strings.Replace(manifest, `"of": "app",`, `"of": "app", "protect": {"user": "staging", "hash": "not-bcrypt"},`, 1)
	if !strings.Contains(manifest, "not-bcrypt") {
		t.Fatalf("manifest edit did not apply:\n%s", manifest)
	}
	e.agent.Files["app-staging"][".windlass.json"] = []byte(manifest)
	if route := stagingRoute(t, e); route != nil {
		t.Errorf("bad hash still routed: %+v", route)
	}
	live := false
	for _, r := range e.agent.Routes {
		live = live || r.Hostname == "app.example.com"
	}
	if !live {
		t.Errorf("live route dropped: %+v", e.agent.Routes)
	}
}
