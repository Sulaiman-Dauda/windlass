package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/deploy"
	"github.com/windlass-dev/windlass/internal/git"
)

func (a *API) gitRoutes(r chi.Router) {
	r.Use(auth.RequireRole("admin"))
	r.Get("/connections", a.handleListGitConnections)
	r.Post("/connections", a.handleCreateGitConnection)
	r.Delete("/connections/{id}", a.handleDeleteGitConnection)
	r.Get("/connections/{id}/repos", a.handleListGitConnectionRepos)
	r.Get("/connections/github/connect", a.handleGitConnectStart)
}

// ---------------------------------------------------------------------------
// One-click GitHub connect
//
// Reuses the instance's GitHub OAuth app (the one configured for sign-in) but
// asks for the repo scope, so admins authorize in the browser instead of
// creating a personal access token. The callback lives under the sign-in
// callback path because GitHub OAuth apps accept redirect URIs only at or
// below the single registered callback URL.

// gitConnectRedirectURI must match between the authorize redirect and the
// code exchange.
func (a *API) gitConnectRedirectURI(r *http.Request) string {
	return a.oauthRedirectURI(r, "github") + "/git"
}

func (a *API) handleGitConnectStart(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.oauthConfig(r.Context(), "github")
	if err != nil || !cfg.Configured() {
		writeError(w, http.StatusBadRequest, "not_configured",
			"configure a GitHub OAuth app in Settings first")
		return
	}

	buf := make([]byte, 16)
	rand.Read(buf)
	state := hex.EncodeToString(buf)
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: state, Path: "/api/v1/auth/oauth",
		MaxAge: 600, HttpOnly: true, Secure: isTLS(r), SameSite: http.SameSiteLaxMode,
	})

	target, err := auth.OAuthAuthorizeURL("github", cfg, a.gitConnectRedirectURI(r), state, "repo")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (a *API) handleGitConnectCallback(w http.ResponseWriter, r *http.Request) {
	// The browser lands here from GitHub, so errors redirect back to
	// Settings instead of rendering JSON.
	fail := func(code string) {
		http.Redirect(w, r, "/settings/git?git_error="+code, http.StatusFound)
	}

	cfg, err := a.oauthConfig(r.Context(), "github")
	if err != nil || !cfg.Configured() {
		fail("not_configured")
		return
	}
	stateCookie, err := r.Cookie(oauthStateCookie)
	if err != nil || stateCookie.Value == "" || r.URL.Query().Get("state") != stateCookie.Value {
		fail("state_mismatch")
		return
	}

	token, err := auth.OAuthAccessToken(r.Context(), "github", cfg, a.gitConnectRedirectURI(r), r.URL.Query().Get("code"))
	if err != nil {
		a.Logger.Warn("github connect", "error", err)
		fail("exchange_failed")
		return
	}
	login, err := auth.GitHubLogin(r.Context(), token)
	if err != nil {
		a.Logger.Warn("github connect", "error", err)
		fail("profile_failed")
		return
	}

	conn, err := a.Git.UpsertConnection(r.Context(), "github", "github-"+login, token)
	if err != nil {
		a.internalError(w, "save github connection", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "git.connection_create", "git_connection", conn.Name, remoteIP(r),
		map[string]string{"via": "oauth"})
	http.Redirect(w, r, "/settings/git?git_connected="+url.QueryEscape(conn.Name), http.StatusFound)
}

func (a *API) handleListGitConnections(w http.ResponseWriter, r *http.Request) {
	list, err := a.Git.ListConnections(r.Context())
	if err != nil {
		a.internalError(w, "list git connections", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) handleCreateGitConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
		Token    string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	conn, err := a.Git.CreateConnection(r.Context(), req.Provider, req.Name, req.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "git.connection_create", "git_connection", conn.Name, remoteIP(r), nil)
	writeJSON(w, http.StatusCreated, conn)
}

// handleListGitConnectionRepos powers the repository picker: the repos the
// connection's token can reach, most recently active first.
func (a *API) handleListGitConnectionRepos(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	repos, err := a.Git.ListRepos(r.Context(), id)
	if errors.Is(err, git.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "connection not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "provider_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

func (a *API) handleDeleteGitConnection(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if err := a.Git.DeleteConnection(r.Context(), id); err != nil {
		a.internalError(w, "delete git connection", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "git.connection_delete", "git_connection",
		chi.URLParam(r, "id"), remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Project git configuration

func (a *API) handleConfigureProjectGit(w http.ResponseWriter, r *http.Request) {
	p, err := a.Projects.Get(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}

	var cfg git.ProjectConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	secret, err := a.Git.Configure(r.Context(), p, cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	branch := cfg.Branch
	if branch == "" {
		branch = "main"
	}
	if err := a.Projects.SetGitMetadata(r.Context(), p.Name, cfg.Repo, branch, cfg.AutoDeploy); err != nil {
		writeError(w, http.StatusInternalServerError, "manifest_write_failed", err.Error())
		return
	}

	// With a connection and auto-deploy on, register the webhook on the
	// provider directly so nothing has to be pasted by hand. Failure is not
	// fatal: the response still carries the secret for manual setup.
	registered := false
	if cfg.ConnectionID > 0 && cfg.AutoDeploy {
		scheme := "http"
		if isTLS(r) {
			scheme = "https"
		}
		if err := a.Git.RegisterWebhook(r.Context(), cfg.ConnectionID, cfg.Repo,
			scheme+"://"+r.Host, p.Name, secret); err != nil {
			a.Logger.Warn("webhook auto-registration", "project", p.Name, "error", err)
		} else {
			registered = true
		}
	}

	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "project.git_configure", "project", p.Name, remoteIP(r),
		map[string]any{"repo": cfg.Repo, "branch": cfg.Branch, "webhook_registered": registered})

	writeJSON(w, http.StatusOK, map[string]any{
		"webhook_secret":     secret,
		"webhook_url":        "/api/v1/webhooks/github/" + p.Name + " (or /webhooks/gitlab/...)",
		"webhook_registered": registered,
	})
}

// ---------------------------------------------------------------------------
// Webhooks (public, HMAC-verified)

type pushPayload struct {
	Ref string `json:"ref"`
}

func (a *API) handleWebhook(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	projectName := chi.URLParam(r, "project")

	p, err := a.Projects.Get(r.Context(), projectName)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "unknown project")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "unreadable body")
		return
	}

	var signature string
	switch provider {
	case "github":
		signature = r.Header.Get("X-Hub-Signature-256")
	case "gitlab":
		signature = r.Header.Get("X-Gitlab-Token")
	}
	if err := a.Git.VerifyWebhook(p, provider, body, signature); err != nil {
		a.Audit.Write(r.Context(), 0, "webhook.rejected", "project", p.Name, remoteIP(r),
			map[string]string{"provider": provider})
		writeError(w, http.StatusForbidden, "invalid_signature", "webhook verification failed")
		return
	}

	// Only pushes to the configured branch deploy.
	var push pushPayload
	_ = json.Unmarshal(body, &push)
	branch := git.PushBranch(push.Ref)
	if p.AutoDeploy == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"result": "ignored", "reason": "auto-deploy disabled"})
		return
	}
	if branch != p.GitBranch.String {
		writeJSON(w, http.StatusOK, map[string]string{"result": "ignored", "reason": "branch mismatch"})
		return
	}

	d, err := a.Deploy.Deploy(r.Context(), p.Name, "webhook")
	if errors.Is(err, deploy.ErrDeployInProgress) {
		writeJSON(w, http.StatusAccepted, map[string]string{"result": "skipped", "reason": "deployment already running"})
		return
	}
	if err != nil {
		a.internalError(w, "webhook deploy", err)
		return
	}
	a.Audit.Write(r.Context(), 0, "webhook.deploy", "project", p.Name, remoteIP(r),
		map[string]any{"provider": provider, "deployment": d.Number})
	writeJSON(w, http.StatusCreated, map[string]any{"result": "deploying", "deployment": d.Number})
}

// ---------------------------------------------------------------------------
// Rollback

func (a *API) handleRollback(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	number, err := strconv.ParseInt(chi.URLParam(r, "number"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid deployment number")
		return
	}

	d, err := a.Deploy.Rollback(r.Context(), name, number)
	switch {
	case errors.Is(err, deploy.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "deployment not found")
		return
	case errors.Is(err, deploy.ErrDeployInProgress):
		writeError(w, http.StatusConflict, "conflict", err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "deployment.rollback", "project", name, remoteIP(r),
		map[string]int64{"target": number, "new": d.Number})
	writeJSON(w, http.StatusCreated, toDeploymentDTO(d))
}
