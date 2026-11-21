package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestAttentionSettingsAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	const path = "/api/v1/system/attention/settings"

	rec := e.do(t, http.MethodGet, path, nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(),
		`{"settings":{"disk_percent":85},"defaults":{"disk_percent":85},"min":50,"max":99}`) {
		t.Fatalf("get = %d: %s", rec.Code, rec.Body.String())
	}

	// The disk is 88% full: a row at the default threshold, cached.
	e.agent.Metrics.DiskTotal, e.agent.Metrics.DiskUsed = 100e9, 88e9
	if rec := e.do(t, http.MethodGet, "/api/v1/system/attention", nil, cookie); !strings.Contains(rec.Body.String(), `"kind":"disk"`) {
		t.Fatalf("attention at 85%% = %s", rec.Body.String())
	}

	for _, bad := range []any{
		map[string]any{"disk_percent": 49},
		map[string]any{"disk_percent": 100},
		map[string]any{"disk_percent": 90.5},
		map[string]any{"disk_percent": "90"},
		map[string]any{},
		map[string]any{"disk_percent": 90, "extra": 1},
	} {
		if rec := e.do(t, http.MethodPut, path, bad, cookie); rec.Code != http.StatusBadRequest {
			t.Errorf("put %v = %d: %s", bad, rec.Code, rec.Body.String())
		}
	}

	rec = e.do(t, http.MethodPut, path, map[string]any{"disk_percent": 90}, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"settings":{"disk_percent":90}`) {
		t.Fatalf("put = %d: %s", rec.Code, rec.Body.String())
	}
	// Saving drops the cached list, so the row goes at once, not after the TTL.
	if rec := e.do(t, http.MethodGet, "/api/v1/system/attention", nil, cookie); strings.Contains(rec.Body.String(), `"kind":"disk"`) {
		t.Fatalf("attention at 90%% = %s", rec.Body.String())
	}

	var audits int
	if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'system.attention_settings'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Errorf("audit entries = %d, want 1", audits)
	}

	// Admin only.
	hashViewer(t, e, context.Background())
	login := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "viewer@example.com", "password": "viewerpassword"})
	viewer := sessionCookie(t, login)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		if rec := e.do(t, method, path, map[string]any{"disk_percent": 60}, viewer); rec.Code != http.StatusForbidden {
			t.Errorf("viewer %s = %d", method, rec.Code)
		}
	}
}
