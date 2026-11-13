package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

func TestDiskCleanupAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)

	rec := e.do(t, http.MethodGet, "/api/v1/system/docker/cleanup", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(),
		`"settings":{"auto":true,"image_days":7,"keep_deployments":5,"build_cache_gb":10}`) ||
		!strings.Contains(rec.Body.String(), `"last":null`) {
		t.Fatalf("get = %d: %s", rec.Code, rec.Body.String())
	}

	for _, bad := range []any{
		map[string]any{"auto": true, "image_days": 0, "keep_deployments": 5, "build_cache_gb": 10},
		map[string]any{"auto": true, "image_days": 7, "keep_deployments": 5, "build_cache_gb": -1},
		map[string]any{"auto": true, "image_days": 7, "keep_deployments": 5, "build_cache_gb": 10, "extra": 1},
		// Missing fields would read as auto off and "clear all cache".
		map[string]any{"image_days": 7, "keep_deployments": 5, "build_cache_gb": 10},
		map[string]any{"auto": true, "image_days": 7, "keep_deployments": 5},
	} {
		if rec := e.do(t, http.MethodPut, "/api/v1/system/docker/cleanup", bad, cookie); rec.Code != http.StatusBadRequest {
			t.Errorf("put %v = %d", bad, rec.Code)
		}
	}
	rec = e.do(t, http.MethodPut, "/api/v1/system/docker/cleanup",
		map[string]any{"auto": false, "image_days": 14, "keep_deployments": 3, "build_cache_gb": 0}, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(),
		`"settings":{"auto":false,"image_days":14,"keep_deployments":3,"build_cache_gb":0}`) {
		t.Fatalf("put = %d: %s", rec.Code, rec.Body.String())
	}

	e.agent.PruneResult = agent.ImagePruneResult{Deleted: 3, ReclaimedBytes: 7e8}
	e.agent.CacheResult = agent.BuildCachePruneResult{ReclaimedBytes: 209e8}
	rec = e.do(t, http.MethodPost, "/api/v1/system/docker/cleanup/run", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("run = %d: %s", rec.Code, rec.Body.String())
	}
	var run struct {
		Trigger    string `json:"trigger"`
		Images     struct{ Deleted int }
		BuildCache struct {
			ReclaimedBytes int64 `json:"reclaimed_bytes"`
		} `json:"build_cache"`
		FinishedAt time.Time `json:"finished_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.Trigger != "manual" || run.Images.Deleted != 3 || run.BuildCache.ReclaimedBytes != 209e8 || run.FinishedAt.IsZero() {
		t.Fatalf("run = %s", rec.Body.String())
	}
	if len(e.agent.CacheKeeps) != 1 || e.agent.CacheKeeps[0] != 0 {
		t.Errorf("cache keeps = %v, want the saved limit of 0", e.agent.CacheKeeps)
	}
	rec = e.do(t, http.MethodGet, "/api/v1/system/docker/cleanup", nil, cookie)
	if !strings.Contains(rec.Body.String(), `"trigger":"manual"`) {
		t.Fatalf("last run not kept: %s", rec.Body.String())
	}

	// Not while a deployment is running: its pulled image has no container yet.
	if _, err := e.sqlDB.Exec(`INSERT INTO projects (id, name) VALUES (90, 'busy')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sqlDB.Exec(`INSERT INTO deployments (project_id, number, status) VALUES (90, 1, 'building')`); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodPost, "/api/v1/system/docker/cleanup/run", nil, cookie)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "busy") || len(e.agent.CacheKeeps) != 1 {
		t.Fatalf("run during a deployment = %d: %s", rec.Code, rec.Body.String())
	}

	var audits int
	if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action IN ('docker.cleanup', 'docker.cleanup_settings')`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 2 {
		t.Errorf("audit entries = %d, want 2", audits)
	}

	// Admin only.
	hashViewer(t, e, context.Background())
	login := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "viewer@example.com", "password": "viewerpassword"})
	viewer := sessionCookie(t, login)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/system/docker/cleanup"},
		{http.MethodPut, "/api/v1/system/docker/cleanup"},
		{http.MethodPost, "/api/v1/system/docker/cleanup/run"},
	} {
		if rec := e.do(t, c.method, c.path, nil, viewer); rec.Code != http.StatusForbidden {
			t.Errorf("viewer %s %s = %d", c.method, c.path, rec.Code)
		}
	}
}
