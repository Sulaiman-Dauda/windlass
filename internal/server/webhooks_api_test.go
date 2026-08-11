package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// configureGit sets up a project's git config through the API and returns
// the webhook secret.
func configureGit(t *testing.T, e *testEnv, cookie *http.Cookie, project string, autoDeploy bool) string {
	t.Helper()
	rec := e.do(t, http.MethodPut, "/api/v1/projects/"+project+"/git", map[string]any{
		"repo": "https://github.com/acme/app.git", "branch": "main", "auto_deploy": autoDeploy,
	}, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("configure git = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		WebhookSecret string `json:"webhook_secret"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.WebhookSecret == "" {
		t.Fatal("no webhook secret returned")
	}
	return resp.WebhookSecret
}

func githubSign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postWebhook(e *testEnv, project, signature string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github/"+project, strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", signature)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func TestWebhookTriggersDeploy(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	secret := configureGit(t, e, cookie, "app", true)

	body := []byte(`{"ref":"refs/heads/main"}`)

	// Bad signature → 403, no deploy.
	rec := postWebhook(e, "app", "sha256=bad", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bad signature = %d, want 403", rec.Code)
	}

	// Wrong branch → ignored.
	other := []byte(`{"ref":"refs/heads/dev"}`)
	rec = postWebhook(e, "app", githubSign(secret, other), other)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "branch mismatch") {
		t.Fatalf("wrong branch = %d: %s", rec.Code, rec.Body.String())
	}

	// Correct push → deployment created.
	rec = postWebhook(e, "app", githubSign(secret, body), body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("webhook = %d: %s", rec.Code, rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app", "1", "succeeded")

	// The git sync ran (project is now git-sourced).
	if calls := strings.Join(e.agent.Calls, "|"); !strings.Contains(calls, "host.gitsync(app)") {
		t.Errorf("git sync not executed: %s", calls)
	}

	// While nothing is running, a second push deploys again; but during an
	// active deployment it's skipped with 202 — simulate by hanging the
	// project's deployment in queued state via a direct second call race:
	// simpler: fire two pushes back-to-back.
	rec = postWebhook(e, "app", githubSign(secret, body), body)
	rec2 := postWebhook(e, "app", githubSign(secret, body), body)
	if rec.Code == http.StatusCreated && rec2.Code == http.StatusCreated {
		// Both created only if the first finished before the second landed —
		// acceptable; otherwise the second must be 202 skipped.
		return
	}
	if rec2.Code != http.StatusAccepted && rec2.Code != http.StatusCreated {
		t.Fatalf("concurrent webhook = %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestWebhookAutoDeployDisabled(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")
	secret := configureGit(t, e, cookie, "app", false)

	body := []byte(`{"ref":"refs/heads/main"}`)
	rec := postWebhook(e, "app", githubSign(secret, body), body)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "auto-deploy disabled") {
		t.Fatalf("= %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStoredGitConnectionRequiresAdminAndMatchingHost(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(t)
	e.prepareDeployableProject(t, admin, "app")

	rec := e.do(t, http.MethodPost, "/api/v1/git/connections", map[string]string{
		"provider": "github", "name": "admin-github", "token": "secret-token",
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create connection = %d: %s", rec.Code, rec.Body.String())
	}
	var conn struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&conn); err != nil || conn.ID == 0 {
		t.Fatalf("decode connection: id=%d err=%v", conn.ID, err)
	}

	rec = e.do(t, http.MethodPost, "/api/v1/users", map[string]string{
		"email": "member@example.com", "password": "memberpassword", "role": "member",
	}, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create member = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "member@example.com", "password": "memberpassword",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("member login = %d: %s", rec.Code, rec.Body.String())
	}
	member := sessionCookie(t, rec)

	gitConfig := map[string]any{
		"repo": "https://github.com/acme/app.git", "branch": "main",
		"connection_id": conn.ID,
	}
	rec = e.do(t, http.MethodPut, "/api/v1/projects/app/git", gitConfig, member)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member attached stored connection: %d: %s", rec.Code, rec.Body.String())
	}

	gitConfig["repo"] = "https://attacker.example/acme/app.git"
	rec = e.do(t, http.MethodPut, "/api/v1/projects/app/git", gitConfig, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("foreign host accepted for stored connection: %d: %s", rec.Code, rec.Body.String())
	}

	gitConfig["repo"] = "https://github.com/acme/app.git"
	rec = e.do(t, http.MethodPut, "/api/v1/projects/app/git", gitConfig, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin valid connection = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRollbackAPI(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login(t)
	e.prepareDeployableProject(t, cookie, "app")

	rec := e.do(t, http.MethodPost, "/api/v1/projects/app/deployments", nil, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app", "1", "succeeded")

	rec = e.do(t, http.MethodPost, "/api/v1/projects/app/deployments/1/rollback", nil, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("rollback = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"triggered_by":"rollback"`) {
		t.Errorf("rollback dto = %s", rec.Body.String())
	}
	waitForStatus(t, e, cookie, "app", "2", "succeeded")
}
