package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestGitConnectOAuthFlow covers the browser-facing half of one-click GitHub
// connect: role gating, the not-configured guard, the authorize redirect
// (repo scope, subpath callback), and state validation on the callback. The
// exchange with GitHub itself is not reachable from unit tests.
func TestGitConnectOAuthFlow(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(t)

	// Viewers cannot start the flow.
	ctx := context.Background()
	hashViewer(t, e, ctx)
	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "viewer@example.com", "password": "viewerpassword",
	})
	viewer := sessionCookie(t, rec)
	if rec := e.do(t, http.MethodGet, "/api/v1/git/connections/github/connect", nil, viewer); rec.Code != http.StatusForbidden {
		t.Errorf("viewer connect = %d, want 403", rec.Code)
	}

	// Without an OAuth app configured the start endpoint refuses.
	rec = e.do(t, http.MethodGet, "/api/v1/git/connections/github/connect", nil, admin)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not_configured") {
		t.Fatalf("connect unconfigured = %d %s, want 400 not_configured", rec.Code, rec.Body.String())
	}

	// Configure the GitHub OAuth app, then the start endpoint redirects to
	// GitHub with the repo scope and the /git subpath callback.
	rec = e.do(t, http.MethodPut, "/api/v1/system/oauth/github",
		map[string]string{"client_id": "cid", "client_secret": "csec"}, admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("configure oauth = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, "/api/v1/git/connections/github/connect", nil, admin)
	if rec.Code != http.StatusFound {
		t.Fatalf("connect start = %d, want 302", rec.Code)
	}
	target, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || target.Host != "github.com" {
		t.Fatalf("redirect target = %q", rec.Header().Get("Location"))
	}
	q := target.Query()
	if q.Get("scope") != "repo" {
		t.Errorf("scope = %q, want repo", q.Get("scope"))
	}
	if !strings.HasSuffix(q.Get("redirect_uri"), "/api/v1/auth/oauth/github/callback/git") {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if q.Get("state") == "" {
		t.Error("authorize URL missing state")
	}

	// A callback whose state does not match the cookie is bounced back to
	// Settings without touching GitHub.
	rec = e.do(t, http.MethodGet, "/api/v1/auth/oauth/github/callback/git?state=bogus&code=x", nil, admin)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/settings/git?git_error=state_mismatch" {
		t.Errorf("callback with bad state = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}
