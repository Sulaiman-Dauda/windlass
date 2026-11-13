package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/cleanup"
)

type cleanupResponse struct {
	Settings cleanup.Settings `json:"settings"`
	Defaults cleanup.Settings `json:"defaults"`
	Last     *cleanup.Result  `json:"last"`
}

func (a *API) handleGetCleanup(w http.ResponseWriter, r *http.Request) {
	last, err := a.Cleanup.Last(r.Context())
	if err != nil {
		a.internalError(w, "load last disk clean-up", err)
		return
	}
	writeJSON(w, http.StatusOK, cleanupResponse{
		Settings: a.Cleanup.Settings(r.Context()), Defaults: cleanup.Defaults, Last: last,
	})
}

// cleanupSettingsRequest makes every field required: a missing auto would
// read as false and a missing build_cache_gb as "clear all cache".
type cleanupSettingsRequest struct {
	Auto            *bool `json:"auto"`
	ImageDays       *int  `json:"image_days"`
	KeepDeployments *int  `json:"keep_deployments"`
	BuildCacheGB    *int  `json:"build_cache_gb"`
}

func (a *API) handleSetCleanup(w http.ResponseWriter, r *http.Request) {
	var body cleanupSettingsRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if body.Auto == nil || body.ImageDays == nil || body.KeepDeployments == nil || body.BuildCacheGB == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "auto, image_days, keep_deployments and build_cache_gb are all required")
		return
	}
	req := cleanup.Settings{Auto: *body.Auto, ImageDays: *body.ImageDays,
		KeepDeployments: *body.KeepDeployments, BuildCacheGB: *body.BuildCacheGB}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := a.Cleanup.SaveSettings(r.Context(), req); err != nil {
		a.internalError(w, "save disk clean-up settings", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "docker.cleanup_settings", "system", "docker", remoteIP(r), req)
	a.handleGetCleanup(w, r)
}

func (a *API) handleRunCleanup(w http.ResponseWriter, r *http.Request) {
	result, err := a.Cleanup.Run(r.Context(), "manual", "")
	var deploying *cleanup.DeployingError
	if errors.Is(err, cleanup.ErrBusy) || errors.As(err, &deploying) {
		writeError(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	if err != nil {
		a.internalError(w, "disk clean-up", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "docker.cleanup", "system", "docker", remoteIP(r), cleanup.AuditDetail(result))
	writeJSON(w, http.StatusOK, result)
}
