package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	spec "github.com/windlass-dev/windlass/api"
	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/proxy"
	"github.com/windlass-dev/windlass/internal/version"
)

type panelDomainResponse struct {
	Hostname       string `json:"hostname"`
	URL            string `json:"url,omitempty"`
	Configured     bool   `json:"configured"`
	ProxyAvailable bool   `json:"proxy_available"`
}

func (a *API) panelDomainResponse(r *http.Request) (panelDomainResponse, error) {
	hostname, err := a.Proxy.PanelDomain(r.Context())
	if err != nil {
		return panelDomainResponse{}, err
	}
	info, _ := a.Proxy.ProxyStatus(r.Context())
	response := panelDomainResponse{Hostname: hostname, Configured: hostname != "",
		ProxyAvailable: info.Available}
	if hostname != "" {
		response.URL = "https://" + hostname
	}
	return response, nil
}

func (a *API) handleGetPanelDomain(w http.ResponseWriter, r *http.Request) {
	response, err := a.panelDomainResponse(r)
	if err != nil {
		a.internalError(w, "get panel domain", err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *API) handleSetPanelDomain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	err := a.Proxy.SetPanelDomain(r.Context(), req.Hostname)
	if errors.Is(err, proxy.ErrInvalidHostname) {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err != nil {
		a.internalError(w, "configure panel domain", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "panel.domain_update", "system", "panel-domain", remoteIP(r),
		map[string]any{"hostname": req.Hostname})
	response, err := a.panelDomainResponse(r)
	if err != nil {
		a.internalError(w, "get panel domain", err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *API) handleImageDiskUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := a.Agent.Docker().ImageDiskUsage(r.Context())
	if err != nil {
		a.internalError(w, "docker image disk usage", err)
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

func (a *API) handlePruneImages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RetentionDays   int `json:"retention_days"`
		KeepDeployments int `json:"keep_deployments"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.RetentionDays < 1 {
		req.RetentionDays = 7
	}
	if req.KeepDeployments < 1 {
		req.KeepDeployments = 5
	}
	if req.RetentionDays > 3650 || req.KeepDeployments > 100 {
		writeError(w, http.StatusBadRequest, "bad_request", "retention_days must be at most 3650 and keep_deployments at most 100")
		return
	}
	digests, err := a.Queries.ListProtectedImageDigests(r.Context(), req.KeepDeployments)
	if err != nil {
		a.internalError(w, "list protected deployment images", err)
		return
	}
	result, err := a.Agent.Docker().PruneImages(r.Context(), agent.ImagePruneReq{
		OlderThanSeconds: int64((time.Duration(req.RetentionDays) * 24 * time.Hour) / time.Second),
		ProtectedDigests: digests,
	})
	if err != nil {
		a.internalError(w, "prune unused images", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "docker.images_prune", "system", "images", remoteIP(r),
		map[string]any{"deleted": result.Deleted, "reclaimed_bytes": result.ReclaimedBytes})
	writeJSON(w, http.StatusOK, result)
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Version: version.Version})
}

func handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.Write(spec.Spec)
}
