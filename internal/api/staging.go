package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/deploy"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/staging"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// deploysThroughStaging reports whether pushes to p go to its staging
// instead, which they do for as long as the staging exists.
func (a *API) deploysThroughStaging(ctx context.Context, p db.Project) (bool, error) {
	_, ok, err := a.Projects.StagingFor(ctx, p.Name)
	return ok, err
}

// decodeStagingRequest reads a create or preflight body. An empty body
// means the defaults. Values the caller saw masked mean "keep live's".
func decodeStagingRequest(w http.ResponseWriter, r *http.Request) (staging.Request, bool) {
	var req staging.Request
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
			return req, false
		}
	}
	for k, v := range req.Env {
		if v == maskedEnvValue {
			delete(req.Env, k)
		}
	}
	return req, true
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (a *API) handleStagingPreflight(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeStagingRequest(w, r)
	if !ok {
		return
	}
	pf, err := a.Staging.Preflight(r.Context(), chi.URLParam(r, "name"), req)
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		a.internalError(w, "staging preflight", err)
		return
	}
	if !auth.CanReadSecrets(r.Context()) {
		for i, f := range pf.Flagged {
			if f.Value != "" {
				pf.Flagged[i].Value = maskedEnvValue
			}
		}
	}
	writeJSON(w, http.StatusOK, pf)
}

func (a *API) handleCreateStaging(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	req, ok := decodeStagingRequest(w, r)
	if !ok {
		return
	}
	res, err := a.Staging.Create(r.Context(), name, req, a.requestOrigin(r))
	switch {
	case errors.Is(err, projects.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	case errors.Is(err, projects.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "conflict", projects.StagingName(name)+" already exists")
		return
	case errors.Is(err, staging.ErrNotAcknowledged):
		writeError(w, http.StatusBadRequest, "acknowledgement_required", err.Error())
		return
	case errors.Is(err, staging.ErrBlocked):
		writeError(w, http.StatusConflict, "staging_blocked", err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "project.staging_create", "project", name, remoteIP(r),
		map[string]any{"staging": res.Project.Name, "branch": req.Branch, "copy_data": req.CopyData,
			"auto_promote": req.AutoPromote, "deployment": res.Deployment,
			"env_edited": sortedKeys(req.Env), "webhook_registered": res.WebhookRegistered})
	writeJSON(w, http.StatusCreated, map[string]any{
		"project": toProjectDTO(res.Project), "result": res,
	})
}

func (a *API) handleGetStaging(w http.ResponseWriter, r *http.Request) {
	st, err := a.Staging.Status(r.Context(), chi.URLParam(r, "name"))
	switch {
	case errors.Is(err, projects.ErrNotFound), errors.Is(err, staging.ErrNoStaging):
		writeError(w, http.StatusNotFound, "not_found", "project has no staging")
		return
	case err != nil:
		a.internalError(w, "staging status", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *API) handleStagingDiff(w http.ResponseWriter, r *http.Request) {
	d, err := a.Staging.Diff(r.Context(), chi.URLParam(r, "name"), chi.URLParam(r, "file"))
	switch {
	case errors.Is(err, staging.ErrNotComposeFile):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	case errors.Is(err, projects.ErrNotFound), errors.Is(err, staging.ErrNoStaging):
		writeError(w, http.StatusNotFound, "not_found", "project has no staging")
		return
	case err != nil:
		a.internalError(w, "staging diff", err)
		return
	}
	if !auth.CanReadSecrets(r.Context()) {
		d.Live, d.ForLive = maskComposeFile(d.Live), maskComposeFile(d.ForLive)
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *API) handleSetStaging(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var req struct {
		AutoPromote *bool `json:"auto_promote"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AutoPromote == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "auto_promote is required")
		return
	}
	err := a.Projects.SetAutoPromote(r.Context(), name, *req.AutoPromote)
	switch {
	case errors.Is(err, projects.ErrNotStaging), errors.Is(err, projects.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "project is not a staging")
		return
	case err != nil:
		a.internalError(w, "set auto-promote", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "project.staging_update", "project", name, remoteIP(r),
		map[string]bool{"auto_promote": *req.AutoPromote})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePromote(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	number, err := strconv.ParseInt(chi.URLParam(r, "number"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid deployment number")
		return
	}
	d, err := a.Deploy.Promote(r.Context(), name, number, "manual")
	switch {
	case errors.Is(err, deploy.ErrNotFound), errors.Is(err, projects.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "deployment not found")
		return
	case errors.Is(err, deploy.ErrDeployInProgress):
		writeError(w, http.StatusConflict, "conflict", "live is already deploying")
		return
	case errors.Is(err, deploy.ErrNotPromotable):
		writeError(w, http.StatusConflict, "not_promotable", err.Error())
		return
	case err != nil:
		a.internalError(w, "promote", err)
		return
	}
	live, _, _, _ := a.Projects.LiveFor(r.Context(), name)
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "deployment.promote", "project", live.Name, remoteIP(r),
		map[string]any{"staging": name, "from": number, "new": d.Number})
	dto := toDeploymentDTO(d)
	dto.PromotedFromNumber = number
	writeJSON(w, http.StatusCreated, dto)
}
