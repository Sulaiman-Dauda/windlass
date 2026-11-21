package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A deploy while one runs answers 202 waiting, the read route shows it to a
// read token, only a deploy-capable caller can cancel it, and every write is
// audited. Rollback keeps its 409.
func TestDeployQueueAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")

	if rec := e.do(t, http.MethodPost, "/api/v1/projects/app/deployments", nil, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("first deploy = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app", "1", "succeeded")

	gate := make(chan struct{})
	e.agent.UpGate = gate
	if rec := e.do(t, http.MethodPost, "/api/v1/projects/app/deployments", nil, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("second deploy = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app", "2", "applying")

	rec := e.do(t, http.MethodPost, "/api/v1/projects/app/deployments", nil, cookie)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"position":1`) {
		t.Fatalf("deploy while running = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodPost, "/api/v1/projects/app/deployments/1/rollback", nil, cookie); rec.Code != http.StatusConflict {
		t.Errorf("rollback while running = %d, want 409", rec.Code)
	}

	read := e.createToken(t, cookie, map[string]any{"name": "r", "scope": "read"})
	rec = e.doBearer(t, http.MethodGet, "/api/v1/projects/app/deployments/waiting", read.Token, nil)
	var got struct {
		Waiting *struct {
			Requests int    `json:"requests"`
			Trigger  string `json:"trigger"`
		} `json:"waiting"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Waiting == nil || got.Waiting.Requests != 1 || got.Waiting.Trigger != "manual" {
		t.Fatalf("waiting = %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.doBearer(t, http.MethodDelete, "/api/v1/projects/app/deployments/waiting", read.Token, nil); rec.Code != http.StatusForbidden {
		t.Errorf("read token cancel = %d, want 403", rec.Code)
	}
	if rec := e.do(t, http.MethodDelete, "/api/v1/projects/app/deployments/waiting", nil, cookie); rec.Code != http.StatusOK {
		t.Fatalf("cancel = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodDelete, "/api/v1/projects/app/deployments/waiting", nil, cookie); rec.Code != http.StatusNotFound {
		t.Errorf("second cancel = %d, want 404", rec.Code)
	}

	// Queue again and let the running one finish: the waiting deploy starts
	// as #3.
	if rec := e.do(t, http.MethodPost, "/api/v1/projects/app/deployments", nil, cookie); rec.Code != http.StatusAccepted {
		t.Fatalf("requeue = %d", rec.Code)
	}
	close(gate)
	waitForStatus(t, e, cookie, "app", "2", "succeeded")
	waitForStatus(t, e, cookie, "app", "3", "succeeded")
	rec = e.do(t, http.MethodGet, "/api/v1/projects/app/deployments/waiting", nil, cookie)
	if strings.TrimSpace(rec.Body.String()) != `{"waiting":null}` {
		t.Errorf("after start, waiting = %s", rec.Body.String())
	}

	for action, want := range map[string]int{"deployment.queue": 2, "deployment.queue_cancel": 1} {
		var n int
		if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ? AND resource_id = 'app'`, action).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s audit rows = %d, want %d", action, n, want)
		}
	}
}
