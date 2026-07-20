package api

// GitHub App manifest flow: two clicks replace the manual OAuth-app setup.
// "Create GitHub App" posts a pre-filled manifest to GitHub; GitHub sends
// back a temporary code; the callback converts it into stored credentials
// that also power sign-in. Installing the app then acts as the git
// connection, and the app's webhook drives auto-deploys for every repo it
// covers.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/deploy"
	"github.com/windlass-dev/windlass/internal/git"
)

const ghAppStateCookie = "windlass_ghapp_state"

func (a *API) requestOrigin(r *http.Request) string {
	scheme := "http"
	if isTLS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (a *API) handleGitHubAppStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.Git.AppConfig(r.Context())
	if errors.Is(err, git.ErrNoApp) {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	if err != nil {
		a.internalError(w, "load github app", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": true,
		"slug":       cfg.Slug,
		"owner":      cfg.Owner,
		"html_url":   cfg.HTMLURL,
	})
}

// manifestPage auto-submits the manifest to GitHub; the browser needs a real
// form POST, so this endpoint renders a tiny self-submitting page.
var manifestPage = template.Must(template.New("m").Parse(`<!doctype html>
<title>Redirecting to GitHub…</title>
<form method="post" action="{{.Action}}">
<input type="hidden" name="manifest" value="{{.Manifest}}">
<noscript><button type="submit">Continue to GitHub</button></noscript>
</form>
<script nonce="{{.Nonce}}">document.forms[0].submit()</script>`))

func (a *API) handleGitHubAppCreate(w http.ResponseWriter, r *http.Request) {
	origin := a.requestOrigin(r)

	// App names are globally unique and capped at 34 chars; derive from the
	// panel host plus a random suffix.
	label := strings.Split(r.Host, ".")[0]
	label = strings.SplitN(label, ":", 2)[0]
	if len(label) > 16 {
		label = label[:16]
	}
	suffix := make([]byte, 2)
	rand.Read(suffix)
	name := fmt.Sprintf("windlass-%s-%s", label, hex.EncodeToString(suffix))

	buf := make([]byte, 16)
	rand.Read(buf)
	state := hex.EncodeToString(buf)
	http.SetCookie(w, &http.Cookie{
		Name: ghAppStateCookie, Value: state, Path: "/api/v1/system/github-app",
		MaxAge: 600, HttpOnly: true, Secure: isTLS(r), SameSite: http.SameSiteLaxMode,
	})

	manifest, _ := json.Marshal(map[string]any{
		"name":         name,
		"url":          origin,
		"redirect_url": origin + "/api/v1/system/github-app/callback",
		"callback_urls": []string{
			origin + "/api/v1/auth/oauth/github/callback",
		},
		"setup_url":       origin + "/api/v1/git/connections/github/setup",
		"setup_on_update": true,
		"public":          false,
		// Repository permissions only: GitHub validates default_permissions
		// against the repository/organization resource list and rejects the
		// whole manifest if it contains an account permission. Account
		// permissions (email_addresses, needed by GET /user/emails for
		// sign-in) can only be added afterwards in the app's settings.
		"default_permissions": map[string]string{
			"contents": "read",
			"metadata": "read",
		},
		"default_events": []string{"push"},
		"hook_attributes": map[string]any{
			"url":    origin + "/api/v1/webhooks/github-app",
			"active": true,
		},
	})

	// This one response needs what the global policy forbids: an inline
	// script (to auto-submit) and a cross-origin form POST (to GitHub).
	// Override the policy here rather than loosening it panel-wide — the
	// script is pinned to a per-response nonce and the form target to
	// github.com, so nothing else gains permission.
	nonceBuf := make([]byte, 16)
	rand.Read(nonceBuf)
	nonce := hex.EncodeToString(nonceBuf)
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; script-src 'nonce-"+nonce+"'; form-action https://github.com")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	manifestPage.Execute(w, map[string]string{
		"Action":   "https://github.com/settings/apps/new?state=" + state,
		"Manifest": string(manifest),
		"Nonce":    nonce,
	})
}

func (a *API) handleGitHubAppCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(code string) {
		http.Redirect(w, r, "/settings/auth?github_app_error="+code, http.StatusFound)
	}

	stateCookie, err := r.Cookie(ghAppStateCookie)
	if err != nil || stateCookie.Value == "" || r.URL.Query().Get("state") != stateCookie.Value {
		fail("state_mismatch")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		fail("missing_code")
		return
	}

	cfg, err := a.Git.ConvertAppManifest(r.Context(), code)
	if err != nil {
		a.Logger.Warn("github app conversion", "error", err)
		fail("conversion_failed")
		return
	}
	// The app's client credentials also power GitHub sign-in.
	if err := a.saveOAuthConfig(r.Context(), "github", auth.OAuthProviderConfig{
		ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
	}); err != nil {
		a.internalError(w, "save oauth config", err)
		return
	}

	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "githubapp.create", "system", cfg.Slug, remoteIP(r), nil)
	http.Redirect(w, r, "/settings/auth?github_app="+url.QueryEscape(cfg.Slug), http.StatusFound)
}

// handleGitHubAppSetup receives the browser after the admin installs the
// app (GitHub's setup_url) and turns the installation into a git connection.
func (a *API) handleGitHubAppSetup(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
	if err != nil || installationID <= 0 {
		http.Redirect(w, r, "/settings/git?git_error=app_install_failed", http.StatusFound)
		return
	}
	conn, err := a.Git.CreateInstallationConnection(r.Context(), installationID)
	if err != nil {
		a.Logger.Warn("github app install", "error", err)
		http.Redirect(w, r, "/settings/git?git_error=app_install_failed", http.StatusFound)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "git.connection_create", "git_connection", conn.Name, remoteIP(r),
		map[string]string{"via": "github-app"})
	http.Redirect(w, r, "/settings/git?git_connected="+url.QueryEscape(conn.Name), http.StatusFound)
}

// ---------------------------------------------------------------------------
// App webhook (public, HMAC-verified)
//
// One endpoint receives pushes for every repository the app covers and
// fans them out to matching projects.

type appPushPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		CloneURL string `json:"clone_url"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
}

// normalizeRepoURL reduces a repo URL to a comparable form.
func normalizeRepoURL(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimSuffix(strings.TrimRight(u, "/"), ".git")
	return u
}

func (a *API) handleAppWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "unreadable body")
		return
	}
	if err := a.Git.VerifyAppWebhook(r.Context(), body, r.Header.Get("X-Hub-Signature-256")); err != nil {
		a.Audit.Write(r.Context(), 0, "webhook.rejected", "system", "github-app", remoteIP(r), nil)
		writeError(w, http.StatusForbidden, "invalid_signature", "webhook verification failed")
		return
	}
	if ev := r.Header.Get("X-GitHub-Event"); ev != "push" {
		writeJSON(w, http.StatusOK, map[string]string{"result": "ignored", "reason": "event " + ev})
		return
	}

	var push appPushPayload
	if err := json.Unmarshal(body, &push); err != nil || push.Repository.CloneURL == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "unreadable payload")
		return
	}
	branch := git.PushBranch(push.Ref)
	repo := normalizeRepoURL(push.Repository.CloneURL)
	repoHTML := normalizeRepoURL(push.Repository.HTMLURL)

	projects, err := a.Projects.List(r.Context())
	if err != nil {
		a.internalError(w, "list projects", err)
		return
	}

	deployed := []string{}
	for _, p := range projects {
		if p.AutoDeploy == 0 || !p.GitRepo.Valid {
			continue
		}
		pr := normalizeRepoURL(p.GitRepo.String)
		if pr != repo && pr != repoHTML {
			continue
		}
		if branch != p.GitBranch.String {
			continue
		}
		d, err := a.Deploy.Deploy(r.Context(), p.Name, "webhook")
		if errors.Is(err, deploy.ErrDeployInProgress) {
			continue
		}
		if err != nil {
			a.Logger.Warn("app webhook deploy", "project", p.Name, "error", err)
			continue
		}
		a.Audit.Write(r.Context(), 0, "webhook.deploy", "project", p.Name, remoteIP(r),
			map[string]any{"provider": "github-app", "deployment": d.Number})
		deployed = append(deployed, p.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": "ok", "deployed": deployed})
}
