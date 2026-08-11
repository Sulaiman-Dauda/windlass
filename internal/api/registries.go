package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/auth"
)

func (a *API) registryRoutes(r chi.Router) {
	r.Use(auth.RequireRole("admin"))
	r.Get("/", a.handleListRegistryCredentials)
	r.Put("/", a.handleSaveRegistryCredential)
	r.Delete("/{id}", a.handleDeleteRegistryCredential)
}

func (a *API) handleListRegistryCredentials(w http.ResponseWriter, r *http.Request) {
	creds, err := a.Registries.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, creds)
}

// handleSaveRegistryCredential stores a credential and immediately logs the
// host in with it.
//
// Applied on save rather than only at the next deployment, for two reasons. It
// tells the admin straight away whether the token actually works, instead of
// leaving them to find out from a failed deploy an hour later. And it means a
// plain `docker compose pull` works from that moment, with or without Windlass
// running, which is the promise in docs/life-without-the-panel.md.
func (a *API) handleSaveRegistryCredential(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host     string `json:"host"`
		Username string `json:"username"`
		Secret   string `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	cred, err := a.Registries.Upsert(r.Context(), req.Host, req.Username, req.Secret)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "registry.credential_save", "registry", cred.Host, remoteIP(r), nil)

	// A credential that does not work is worth saying so about now. It is still
	// stored: the token may be right and the daemon temporarily unreachable,
	// and throwing away what somebody typed would be worse than telling them.
	if err := a.Registries.Apply(r.Context(), a.Agent.Docker()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"credential": cred,
			"warning":    err.Error(),
		})
		return
	}

	refreshed, err := a.Registries.List(r.Context())
	if err == nil {
		for _, c := range refreshed {
			if c.Host == cred.Host {
				cred = c
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"credential": cred})
}

func (a *API) handleDeleteRegistryCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if err := a.Registries.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "registry.credential_delete", "registry", strconv.FormatInt(id, 10), remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}
