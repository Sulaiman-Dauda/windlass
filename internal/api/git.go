package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "project.git_configure", "project", p.Name, remoteIP(r),
		map[string]string{"repo": cfg.Repo, "branch": cfg.Branch})

	cfg.WebhookSecret = secret
	writeJSON(w, http.StatusOK, map[string]string{
		"webhook_secret": secret,
		"webhook_url":    "/api/v1/webhooks/github/" + p.Name + " (or /webhooks/gitlab/...)",
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
