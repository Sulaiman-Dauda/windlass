package server

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestTagSuggestionAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	e.agent.HubTags = map[string][]string{"library/nginx": {"latest", "1.27.3", "1.27", "1.26.2", "1.25.4"}}
	const path = "/api/v1/projects/app/deployments/1/tag-suggestion"

	// A succeeded deployment has nothing to suggest.
	if rec := e.do(t, http.MethodPost, "/api/v1/projects/app/deployments", nil, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("deploy = %d", rec.Code)
	}
	waitForStatus(t, e, cookie, "app", "1", "succeeded")
	if rec := e.do(t, http.MethodGet, path, nil, cookie); rec.Code != http.StatusNotFound {
		t.Fatalf("succeeded = %d: %s", rec.Code, rec.Body.String())
	}

	// Deployment 2 fails on a tag Docker Hub does not have.
	e.agent.Fail["compose.pull"] = errors.New("Error response from daemon: manifest unknown: manifest unknown")
	e.do(t, http.MethodPost, "/api/v1/projects/app/deployments", nil, cookie)
	waitForStatus(t, e, cookie, "app", "2", "failed")
	// Compose v5 names the image only on its progress line, which the fake
	// pull does not write; the error line it ends with names none. The
	// ghcr.io image is skipped: only Docker Hub is asked.
	for _, line := range []string{" Image ghcr.io/acme/x:1 Error manifest unknown", " Image nginx:1.27.9 Error manifest unknown: manifest unknown"} {
		if _, err := e.sqlDB.Exec(`INSERT INTO deployment_events (deployment_id, seq, type, message)
			SELECT d.id, (SELECT COALESCE(MAX(seq), 0) + 1 FROM deployment_events WHERE deployment_id = d.id), 'log', ?
			FROM deployments d JOIN projects p ON p.id = d.project_id WHERE p.name = 'app' AND d.number = 2`, line); err != nil {
			t.Fatal(err)
		}
	}
	path2 := strings.Replace(path, "/1/", "/2/", 1)
	rec := e.do(t, http.MethodGet, path2, nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(),
		`{"images":[{"image":"nginx:1.27.9","tag":"1.27.9","repository":"library/nginx","tags":["1.27.3","1.27","1.26.2","1.25.4"]}]}`) {
		t.Fatalf("suggestion = %d: %s", rec.Code, rec.Body.String())
	}

	// Docker Hub unreachable is a 502, not a 404.
	e.agent.Fail["docker.hubtags"] = errors.New("dial tcp: no route")
	if rec := e.do(t, http.MethodGet, path2, nil, cookie); rec.Code != http.StatusBadGateway {
		t.Fatalf("hub down = %d", rec.Code)
	}
	delete(e.agent.Fail, "docker.hubtags")

	// Admin turns it off: 404, and Docker Hub is not asked.
	const settings = "/api/v1/system/deploy/settings"
	if rec := e.do(t, http.MethodGet, settings, nil, cookie); !strings.Contains(rec.Body.String(), `{"tag_suggestions":true}`) {
		t.Fatalf("settings = %s", rec.Body.String())
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"tag_suggestions": "no"}, map[string]any{"tag_suggestions": false, "x": 1}} {
		if rec := e.do(t, http.MethodPut, settings, bad, cookie); rec.Code != http.StatusBadRequest {
			t.Errorf("put %v = %d", bad, rec.Code)
		}
	}
	if rec := e.do(t, http.MethodPut, settings, map[string]any{"tag_suggestions": false}, cookie); !strings.Contains(rec.Body.String(), `{"tag_suggestions":false}`) {
		t.Fatalf("put = %d: %s", rec.Code, rec.Body.String())
	}
	before := strings.Count(strings.Join(e.agent.Calls, "|"), "docker.hubtags")
	if rec := e.do(t, http.MethodGet, path2, nil, cookie); rec.Code != http.StatusNotFound {
		t.Fatalf("off = %d", rec.Code)
	}
	if after := strings.Count(strings.Join(e.agent.Calls, "|"), "docker.hubtags"); after != before {
		t.Fatal("Docker Hub asked with the setting off")
	}
	var audits int
	if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'system.deploy_settings'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits = %d, %v", audits, err)
	}
}
