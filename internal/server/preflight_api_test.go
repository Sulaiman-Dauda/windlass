package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

type preflightBody struct {
	Checks []struct{ ID, Status, Title string } `json:"checks"`
}

func (b preflightBody) status(id string) string {
	for _, c := range b.Checks {
		if c.ID == id {
			return c.Status
		}
	}
	return ""
}

func getPreflight(t *testing.T, e *testEnv, token string, cookies ...*http.Cookie) (*httptest.ResponseRecorder, preflightBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/preflight", nil)
	req.Host = "203.0.113.7:8080"
	if token != "" {
		req.Header.Set("X-Setup-Token", token)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	var body preflightBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
	}
	return rec, body
}

func TestPreflightNeedsSetupTokenThenAdmin(t *testing.T) {
	e := newTestEnv(t)
	e.agent.Node = agent.NodeInfo{DockerVersion: "28.1", ComposeVersion: "2.36", CaddyVersion: "2.10"}
	e.agent.Metrics = agent.HostMetrics{DiskUsed: 98 << 30, DiskTotal: 100 << 30}
	token := e.setupToken

	if rec, _ := getPreflight(t, e, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("no token = %d", rec.Code)
	}
	if rec, _ := getPreflight(t, e, "wrong"); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token = %d", rec.Code)
	}
	rec, body := getPreflight(t, e, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("token = %d: %s", rec.Code, rec.Body.String())
	}
	for id, want := range map[string]string{"docker": "ok", "compose": "ok", "caddy": "ok",
		"port_80": "ok", "port_443": "ok", "panel_https": "warn", "disk": "warn"} {
		if got := body.status(id); got != want {
			t.Errorf("%s = %q, want %q in %s", id, got, want, rec.Body.String())
		}
	}

	// After the claim the token is dead and an admin reads it instead.
	cookie := e.login(t)
	if rec, _ := getPreflight(t, e, token); rec.Code != http.StatusForbidden {
		t.Fatalf("token after claim = %d", rec.Code)
	}
	if rec, _ := getPreflight(t, e, "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("admin = %d", rec.Code)
	}
	hashViewer(t, e, context.Background())
	login := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "viewer@example.com", "password": "viewerpassword"})
	if rec, _ := getPreflight(t, e, "", sessionCookie(t, login)); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer = %d", rec.Code)
	}
}

func TestPreflightReportsMissingPieces(t *testing.T) {
	e := newTestEnv(t)
	e.agent.Node = agent.NodeInfo{}
	rec, body := getPreflight(t, e, e.setupToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight = %d", rec.Code)
	}
	for id, want := range map[string]string{"docker": "fail", "compose": "fail", "caddy": "warn", "port_80": ""} {
		if got := body.status(id); got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
	if strings.Contains(strings.Join(e.agent.Calls, " "), "host.httpcheck") {
		t.Error("ports checked without Caddy")
	}
}

func TestPreflightPortRefused(t *testing.T) {
	e := newTestEnv(t)
	e.agent.Node = agent.NodeInfo{DockerVersion: "28.1", ComposeVersion: "2.36", CaddyVersion: "2.10"}
	e.agent.Fail["host.httpcheck"] = fmt.Errorf("dial: %w", syscall.ECONNREFUSED)
	_, body := getPreflight(t, e, e.setupToken)
	if body.status("port_80") != "warn" || body.status("port_443") != "warn" {
		t.Fatalf("refused ports = %+v", body.Checks)
	}
}
