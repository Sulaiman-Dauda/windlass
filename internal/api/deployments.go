package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/deploy"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/store/db"
)

type deploymentDTO struct {
	ID          int64  `json:"id"`
	Number      int64  `json:"number"`
	Status      string `json:"status"`
	TriggeredBy string `json:"triggered_by"`
	GitCommit   string `json:"git_commit,omitempty"`
	Error       string `json:"error,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	FinishedAt  string `json:"finished_at,omitempty"`
	CreatedAt   string `json:"created_at"`
}

func toDeploymentDTO(d db.Deployment) deploymentDTO {
	return deploymentDTO{
		ID:          d.ID,
		Number:      d.Number,
		Status:      d.Status,
		TriggeredBy: d.TriggeredBy,
		GitCommit:   d.GitCommit.String,
		Error:       d.Error.String,
		StartedAt:   d.StartedAt.String,
		FinishedAt:  d.FinishedAt.String,
		CreatedAt:   d.CreatedAt,
	}
}

func (a *API) deploymentRoutes(r chi.Router) {
	r.Get("/", a.handleListDeployments)
	r.Get("/{number}", a.handleGetDeployment)
	r.Get("/{number}/events", a.handleDeploymentEvents)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole("member"))
		r.Post("/", a.handleCreateDeployment)
	})
}

func (a *API) handleCreateDeployment(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	d, err := a.Deploy.Deploy(r.Context(), name, "manual")
	switch {
	case errors.Is(err, projects.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	case errors.Is(err, deploy.ErrDeployInProgress):
		writeError(w, http.StatusConflict, "conflict", err.Error())
		return
	case err != nil:
		a.internalError(w, "create deployment", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "deployment.create", "project", name, remoteIP(r),
		map[string]int64{"number": d.Number})
	writeJSON(w, http.StatusCreated, toDeploymentDTO(d))
}

func (a *API) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	list, err := a.Deploy.List(r.Context(), chi.URLParam(r, "name"), 50)
	if err != nil {
		a.internalError(w, "list deployments", err)
		return
	}
	out := make([]deploymentDTO, 0, len(list))
	for _, d := range list {
		out = append(out, toDeploymentDTO(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) resolveDeployment(w http.ResponseWriter, r *http.Request) (db.Deployment, bool) {
	number, err := strconv.ParseInt(chi.URLParam(r, "number"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid deployment number")
		return db.Deployment{}, false
	}
	d, err := a.Deploy.Get(r.Context(), chi.URLParam(r, "name"), number)
	if errors.Is(err, deploy.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "deployment not found")
		return db.Deployment{}, false
	}
	if err != nil {
		a.internalError(w, "get deployment", err)
		return db.Deployment{}, false
	}
	return d, true
}

func (a *API) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	d, ok := a.resolveDeployment(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toDeploymentDTO(d))
}

func (a *API) handleDeploymentEvents(w http.ResponseWriter, r *http.Request) {
	d, ok := a.resolveDeployment(w, r)
	if !ok {
		return
	}
	a.streamDeploymentEvents(w, r, d.ID, lastEventID(r))
}

// ---------------------------------------------------------------------------
// Project actions (start/stop/restart) — direct compose operations, no
// deployment record; these don't change what is deployed, only whether it
// runs.

func (a *API) handleProjectAction(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	action := chi.URLParam(r, "action")

	if _, err := a.Projects.Get(r.Context(), name); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	var err error
	switch action {
	case "start":
		err = a.Agent.Compose().Up(ctx, agentUpReq(name), nil)
	case "stop":
		err = a.Agent.Compose().Stop(ctx, name, nil)
	case "restart":
		err = a.Agent.Compose().Restart(ctx, name, nil)
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown action")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "compose_failed", err.Error())
		return
	}

	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "project."+action, "project", name, remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}

// handleProjectServices reports live service status from compose ps.
func (a *API) handleProjectServices(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if _, err := a.Projects.Get(r.Context(), name); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	statuses, err := a.Agent.Compose().PS(r.Context(), name)
	if err != nil {
		// Compose unavailable or never deployed — an empty list with a note
		// beats a 500 (graceful degradation).
		writeJSON(w, http.StatusOK, map[string]any{"services": []any{}, "note": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": statuses})
}
