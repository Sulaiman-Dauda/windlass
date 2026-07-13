package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/backups"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/store/db"
)

type backupDTO struct {
	ID          int64  `json:"id"`
	Kind        string `json:"kind"`
	Destination string `json:"destination"`
	Size        int64  `json:"size"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	CreatedAt   string `json:"created_at"`
}

func toBackupDTO(b db.Backup) backupDTO {
	return backupDTO{
		ID: b.ID, Kind: b.Kind, Destination: b.Destination, Size: b.Size,
		Status: b.Status, Error: b.Error.String, CreatedAt: b.CreatedAt,
	}
}

func (a *API) backupRoutes(r chi.Router) {
	r.Get("/", a.handleListBackups)
	r.Get("/schedule", a.handleGetBackupSchedule)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole("member"))
		r.Post("/", a.handleCreateBackup)
		r.Post("/{id}/restore", a.handleRestoreBackup)
		r.Put("/schedule", a.handleSetBackupSchedule)
	})
}

func (a *API) handleListBackups(w http.ResponseWriter, r *http.Request) {
	list, err := a.Backups.List(r.Context(), chi.URLParam(r, "name"))
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		a.internalError(w, "list backups", err)
		return
	}
	out := make([]backupDTO, 0, len(list))
	for _, b := range list {
		out = append(out, toBackupDTO(b))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Destination string `json:"destination"`
	}
	json.NewDecoder(r.Body).Decode(&req) // empty body = local

	name := chi.URLParam(r, "name")
	b, err := a.Backups.Run(r.Context(), name, "manual", req.Destination)
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		// The row records the failure; surface both.
		writeJSON(w, http.StatusBadGateway, toBackupDTO(b))
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "backup.create", "project", name, remoteIP(r), nil)
	writeJSON(w, http.StatusCreated, toBackupDTO(b))
}

func (a *API) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid backup id")
		return
	}

	err = a.Backups.Restore(r.Context(), name, id)
	if errors.Is(err, backups.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "restore_failed", err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "backup.restore", "project", name, remoteIP(r),
		map[string]int64{"backup": id})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleGetBackupSchedule(w http.ResponseWriter, r *http.Request) {
	sched, err := a.Backups.GetSchedule(r.Context(), chi.URLParam(r, "name"))
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		a.internalError(w, "get schedule", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"interval":        sched.Interval,
		"destination":     sched.Destination,
		"retention_count": sched.RetentionCount,
		"enabled":         sched.Enabled != 0,
	})
}

func (a *API) handleSetBackupSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Interval       string `json:"interval"`
		Destination    string `json:"destination"`
		RetentionCount int64  `json:"retention_count"`
		Enabled        bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	name := chi.URLParam(r, "name")
	err := a.Backups.SetSchedule(r.Context(), name, req.Interval, req.Destination, req.RetentionCount, req.Enabled)
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "backup.schedule", "project", name, remoteIP(r), req)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// S3 settings (admin)

func (a *API) handleGetS3Config(w http.ResponseWriter, r *http.Request) {
	configured, endpoint, bucket := a.Backups.S3ConfigStatus(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": configured, "endpoint": endpoint, "bucket": bucket,
	})
}

func (a *API) handleSetS3Config(w http.ResponseWriter, r *http.Request) {
	var cfg backups.S3Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if !cfg.Configured() {
		writeError(w, http.StatusBadRequest, "bad_request", "endpoint, bucket, access_key, and secret_key are required")
		return
	}
	if err := a.Backups.SetS3Config(r.Context(), cfg); err != nil {
		a.internalError(w, "set s3 config", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "backup.s3_configure", "", "", remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}
