// Package api contains the /api/v1 HTTP handlers. Handlers stay thin:
// validate the request, call a service, write a response.
package api

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/audit"
	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/projects"
)

type API struct {
	Auth     *auth.Service
	Audit    *audit.Log
	Projects *projects.Service
	Logger   *slog.Logger
}

func (a *API) Routes(r chi.Router) {
	// Public
	r.Get("/system/health", handleHealth)
	r.Get("/auth/status", a.handleAuthStatus)
	r.Post("/auth/setup", a.handleSetup)
	r.Post("/auth/login", a.handleLogin)
	r.Post("/auth/logout", a.handleLogout)

	// Authenticated
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/auth/me", a.handleMe)
		r.Route("/projects", a.projectRoutes)
	})
}
