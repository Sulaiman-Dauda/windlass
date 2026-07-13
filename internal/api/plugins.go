package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/plugins"
)

func (a *API) pluginRoutes(r chi.Router) {
	r.Get("/", a.handleListPlugins)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole("admin"))
		r.Post("/{name}/enable", a.handleEnablePlugin)
		r.Post("/{name}/disable", a.handleDisablePlugin)
	})
	r.HandleFunc("/{name}/proxy", a.handlePluginProxy)
	r.HandleFunc("/{name}/proxy/*", a.handlePluginProxy)
}

func (a *API) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	list, err := a.Plugins.List(r.Context())
	if err != nil {
		a.internalError(w, "list plugins", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) handleEnablePlugin(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	err := a.Plugins.Enable(r.Context(), name)
	if errors.Is(err, plugins.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "plugin not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "plugin_failed", err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "plugin.enable", "plugin", name, remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleDisablePlugin(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := a.Plugins.Disable(r.Context(), name); err != nil {
		a.internalError(w, "disable plugin", err)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "plugin.disable", "plugin", name, remoteIP(r), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handlePluginProxy(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	// Strip our prefix so plugins see clean paths.
	prefix := "/api/v1/plugins/" + name + "/proxy"
	r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	a.Plugins.Proxy(name, w, r)
}
