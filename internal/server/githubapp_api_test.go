package server

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// TestGitHubAppManifestPage covers the browser-facing start of the two-click
// GitHub App flow. The manifest page is the one response that must escape the
// panel's global CSP, it auto-submits an inline script and POSTs
// cross-origin to github.com, so the scoped override is asserted here; the
// global policy blocks both and silently broke this page once already.
func TestGitHubAppManifestPage(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(t)

	rec := e.do(t, http.MethodGet, "/api/v1/system/github-app/create", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("create page = %d: %s", rec.Code, rec.Body.String())
	}

	csp := rec.Header().Get("Content-Security-Policy")
	nonce := regexp.MustCompile(`script-src 'nonce-([a-f0-9]+)'`).FindStringSubmatch(csp)
	if nonce == nil {
		t.Fatalf("CSP does not permit a nonced script: %q", csp)
	}
	if !strings.Contains(csp, "form-action https://github.com") {
		t.Errorf("CSP does not permit the form POST to GitHub: %q", csp)
	}

	body := rec.Body.String()
	// The script tag must carry the same nonce the header allows, or the
	// browser blocks it exactly as the global policy did.
	if !strings.Contains(body, `<script nonce="`+nonce[1]+`">`) {
		t.Errorf("script tag nonce does not match the CSP nonce (%s): %s", nonce[1], body)
	}
	if !strings.Contains(body, `action="https://github.com/settings/apps/new?state=`) {
		t.Errorf("form does not post to GitHub: %s", body)
	}

	// The manifest itself must carry the URLs and permissions the rest of
	// the flow depends on.
	manifest := regexp.MustCompile(`name="manifest" value="([^"]*)"`).FindStringSubmatch(body)
	if manifest == nil {
		t.Fatal("no manifest field in the form")
	}
	var parsed struct {
		RedirectURL string            `json:"redirect_url"`
		SetupURL    string            `json:"setup_url"`
		Permissions map[string]string `json:"default_permissions"`
		Events      []string          `json:"default_events"`
		Hook        struct {
			URL string `json:"url"`
		} `json:"hook_attributes"`
	}
	if err := json.Unmarshal([]byte(htmlUnescape(manifest[1])), &parsed); err != nil {
		t.Fatalf("manifest is not valid JSON: %v (%s)", err, manifest[1])
	}
	if !strings.HasSuffix(parsed.RedirectURL, "/api/v1/system/github-app/callback") {
		t.Errorf("redirect_url = %q", parsed.RedirectURL)
	}
	if !strings.HasSuffix(parsed.SetupURL, "/api/v1/git/connections/github/setup") {
		t.Errorf("setup_url = %q", parsed.SetupURL)
	}
	if !strings.HasSuffix(parsed.Hook.URL, "/api/v1/webhooks/github-app") {
		t.Errorf("hook url = %q", parsed.Hook.URL)
	}
	for _, want := range []string{"contents", "metadata"} {
		if parsed.Permissions[want] != "read" {
			t.Errorf("permission %q = %q, want read", want, parsed.Permissions[want])
		}
	}
	// GitHub validates default_permissions against the repository resource
	// list and rejects the entire manifest if an account permission appears
	// there, which is exactly how this flow broke once.
	for _, account := range []string{"email_addresses", "emails", "profile", "followers", "gpg_keys"} {
		if _, present := parsed.Permissions[account]; present {
			t.Errorf("account permission %q in default_permissions; GitHub rejects the manifest", account)
		}
	}
	if len(parsed.Events) != 1 || parsed.Events[0] != "push" {
		t.Errorf("default_events = %v, want [push]", parsed.Events)
	}
}

// TestGitHubAppCSPScoped proves the override is confined to the manifest page
// and does not weaken the policy on ordinary responses.
func TestGitHubAppCSPScoped(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login(t)

	rec := e.do(t, http.MethodGet, "/api/v1/system/github-app", nil, admin)
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "github.com") {
		t.Errorf("status endpoint CSP was widened: %q", csp)
	}
}

func htmlUnescape(s string) string {
	r := strings.NewReplacer("&#34;", `"`, "&quot;", `"`, "&amp;", "&", "&lt;", "<", "&gt;", ">", "&#39;", "'")
	return r.Replace(s)
}
