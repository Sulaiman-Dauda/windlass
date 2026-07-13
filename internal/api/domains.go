package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/proxy"
)

func (a *API) domainRoutes(r chi.Router) {
	r.Get("/", a.handleListDomains)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole("member"))
		r.Post("/", a.handleCreateDomain)
		r.Delete("/{hostname}", a.handleDeleteDomain)
	})
}

func (a *API) handleListDomains(w http.ResponseWriter, r *http.Request) {
	p, err := a.Projects.Get(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	list, err := a.Proxy.List(r.Context(), p.ID)
	if err != nil {
		a.internalError(w, "list domains", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createDomainRequest struct {
	Hostname      string `json:"hostname"`
	Service       string `json:"service"`
	ContainerPort int64  `json:"container_port"`
}

func (a *API) handleCreateDomain(w http.ResponseWriter, r *http.Request) {
	p, err := a.Projects.Get(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}

	var req createDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	d, err := a.Proxy.Add(r.Context(), p.ID, req.Hostname, req.Service, req.ContainerPort)
	if errors.Is(err, proxy.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "hostname already in use")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "domain.create", "domain", d.Hostname, remoteIP(r),
		map[string]any{"project": p.Name, "service": req.Service, "port": req.ContainerPort})
	writeJSON(w, http.StatusCreated, d)
}

func (a *API) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	hostname := chi.URLParam(r, "hostname")

	err := a.Proxy.Delete(r.Context(), name, hostname)
	if errors.Is(err, proxy.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "domain not found")
		return
	}
	if err != nil {
		a.internalError(w, "delete domain", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "domain.delete", "domain", hostname, remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleProxyStatus(w http.ResponseWriter, r *http.Request) {
	info, err := a.Proxy.ProxyStatus(r.Context())
	if err != nil {
		a.internalError(w, "proxy status", err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}
