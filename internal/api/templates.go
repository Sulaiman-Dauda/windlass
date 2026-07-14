package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/dbtemplates"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/proxy"
)

func (a *API) templateRoutes(r chi.Router) {
	r.Get("/", a.handleListTemplates)
	r.With(auth.RequireRole("member")).Post("/{key}", a.handleCreateFromTemplate)
}

func (a *API) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, dbtemplates.List())
}

type createTemplateRequest struct {
	Name     string `json:"name"`
	HostPort int    `json:"host_port,omitempty"`
	Domain   string `json:"domain,omitempty"`
}

// handleCreateFromTemplate renders the template into a normal project with
// generated credentials and immediately deploys it. App templates (those with a
// route) are also attached to the requested domain so they come up on HTTPS.
func (a *API) handleCreateFromTemplate(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")

	var req createTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = key
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))

	tmpl, ok := dbtemplates.Get(key)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "unknown template")
		return
	}
	if tmpl.Route != nil && req.Domain == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "this template requires a domain")
		return
	}

	compose, env, err := dbtemplates.Render(key, req.Name, req.Domain, req.HostPort)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	p, err := a.Projects.Create(r.Context(), projects.CreateReq{
		Name: req.Name, Source: "template", Compose: compose,
	})
	if errors.Is(err, projects.ErrAlreadyExists) {
		writeError(w, http.StatusConflict, "conflict", "a project with this name already exists")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := a.Projects.SetEnv(r.Context(), p.Name, env); err != nil {
		a.internalError(w, "template env", err)
		return
	}

	// App templates get their domain attached before deploy; the deployment's
	// completion event then drives the Caddy sync that brings the route live.
	if tmpl.Route != nil {
		_, err := a.Proxy.Add(r.Context(), p.ID, req.Domain, tmpl.Route.Service, tmpl.Route.ContainerPort)
		if errors.Is(err, proxy.ErrConflict) {
			writeError(w, http.StatusConflict, "conflict", "that domain is already in use")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}

	d, err := a.Deploy.Deploy(r.Context(), p.Name, "manual")
	if err != nil {
		a.internalError(w, "template deploy", err)
		return
	}

	user, _ := auth.UserFrom(r.Context())
	a.Audit.Write(r.Context(), user.ID, "template.create", "project", p.Name, remoteIP(r),
		map[string]string{"template": key})
	writeJSON(w, http.StatusCreated, map[string]any{
		"project":    toProjectDTO(p),
		"deployment": toDeploymentDTO(d),
		// Credentials live in the project's Environment tab.
	})
}
