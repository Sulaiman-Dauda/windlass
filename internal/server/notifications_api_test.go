package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestNotificationChannelsAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	const base = "/api/v1/notifications/channels"

	rec := e.do(t, http.MethodPost, base, map[string]any{
		"name": "ops", "kind": "slack", "events": []string{"deploy_failed"},
		"config": map[string]string{"url": "https://hooks.slack.com/services/T/B/secret-token"},
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	var ch struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)

	rec = e.do(t, http.MethodGet, base, nil, cookie)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "secret-token") || !strings.Contains(rec.Body.String(), `"url_set":true`) {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodPost, base, map[string]any{"name": "x", "kind": "pager"}, cookie); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown kind = %d", rec.Code)
	}
	if rec := e.do(t, http.MethodPut, base+"/999", map[string]any{"name": "x"}, cookie); rec.Code != http.StatusNotFound {
		t.Errorf("update missing = %d", rec.Code)
	}

	// A token may read but never change where alerts go.
	tok := e.createToken(t, cookie, map[string]any{"name": "a", "scope": "admin"})
	if rec := e.doBearer(t, http.MethodGet, base, tok.Token, nil); rec.Code != http.StatusOK {
		t.Errorf("token list = %d", rec.Code)
	}
	if rec := e.doBearer(t, http.MethodPost, base, tok.Token, map[string]any{"name": "x"}); rec.Code != http.StatusForbidden {
		t.Errorf("token create = %d", rec.Code)
	}

	if rec := e.do(t, http.MethodDelete, base+"/"+itoa(ch.ID), nil, cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	var audits int
	_ = e.sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action LIKE 'notification.%'`).Scan(&audits)
	if audits != 2 {
		t.Errorf("audit entries = %d, want 2", audits)
	}

	hashViewer(t, e, context.Background())
	login := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "viewer@example.com", "password": "viewerpassword"})
	viewer := sessionCookie(t, login)
	for _, p := range []string{base, "/api/v1/notifications/deliveries", "/api/v1/notifications/kinds"} {
		if rec := e.do(t, http.MethodGet, p, nil, viewer); rec.Code != http.StatusForbidden {
			t.Errorf("viewer %s = %d", p, rec.Code)
		}
	}
}
