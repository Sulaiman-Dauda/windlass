package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestBackupRestoreAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")

	// Write a distinctive compose file, back up, then wreck it.
	rec := e.do(t, http.MethodPut, "/api/v1/projects/app/files/compose.yaml",
		map[string]string{"content": "services:\n  web:\n    image: original:1\n"}, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Body.String())
	}

	rec = e.do(t, http.MethodPost, "/api/v1/projects/app/backups", nil, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("backup = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"done"`) {
		t.Fatalf("backup body = %s", rec.Body.String())
	}

	rec = e.do(t, http.MethodPut, "/api/v1/projects/app/files/compose.yaml",
		map[string]string{"content": "services:\n  web:\n    image: broken:9\n"}, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Body.String())
	}

	// Restore backup #1.
	rec = e.do(t, http.MethodPost, "/api/v1/projects/app/backups/1/restore", nil, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("restore = %d: %s", rec.Code, rec.Body.String())
	}

	data, err := e.agent.FS().ReadFile(context.Background(), "app", "compose.yaml")
	if err != nil || !strings.Contains(string(data), "original:1") {
		t.Fatalf("after restore: %q, %v", data, err)
	}

	// List shows the backup.
	rec = e.do(t, http.MethodGet, "/api/v1/projects/app/backups", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"kind":"manual"`) {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}

	// Restoring a nonexistent backup 404s.
	rec = e.do(t, http.MethodPost, "/api/v1/projects/app/backups/999/restore", nil, cookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("restore missing = %d", rec.Code)
	}
}

func TestBackupScheduleAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")

	rec := e.do(t, http.MethodPut, "/api/v1/projects/app/backups/schedule", map[string]any{
		"interval": "daily", "destination": "local", "retention_count": 5, "enabled": true,
	}, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("set schedule = %d: %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, http.MethodGet, "/api/v1/projects/app/backups/schedule", nil, cookie)
	body := rec.Body.String()
	for _, want := range []string{`"interval":"daily"`, `"retention_count":5`, `"enabled":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("schedule missing %s: %s", want, body)
		}
	}

	// Invalid interval rejected.
	rec = e.do(t, http.MethodPut, "/api/v1/projects/app/backups/schedule", map[string]any{
		"interval": "yearly",
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad interval = %d", rec.Code)
	}
}

func TestS3ConfigAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)

	// Unconfigured by default; secrets never leak from GET.
	rec := e.do(t, http.MethodGet, "/api/v1/system/backups/s3", nil, cookie)
	if !strings.Contains(rec.Body.String(), `"configured":false`) {
		t.Fatalf("initial = %s", rec.Body.String())
	}

	rec = e.do(t, http.MethodPut, "/api/v1/system/backups/s3", map[string]string{
		"endpoint": "https://s3.example.com", "region": "auto", "bucket": "backups",
		"access_key": "AKIA123", "secret_key": "verysecret",
	}, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("set = %d: %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, http.MethodGet, "/api/v1/system/backups/s3", nil, cookie)
	body := rec.Body.String()
	if !strings.Contains(body, `"configured":true`) || !strings.Contains(body, "s3.example.com") {
		t.Fatalf("after set = %s", body)
	}
	if strings.Contains(body, "verysecret") || strings.Contains(body, "AKIA123") {
		t.Errorf("credentials leaked: %s", body)
	}
}
