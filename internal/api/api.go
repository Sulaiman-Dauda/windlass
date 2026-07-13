// Package api contains the /api/v1 HTTP handlers. Handlers stay thin:
// validate the request, call a service, write a response.
package api

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/audit"
	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/backups"
	"github.com/windlass-dev/windlass/internal/deploy"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/git"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/proxy"
	"github.com/windlass-dev/windlass/internal/update"
)

type API struct {
	Auth     *auth.Service
	Audit    *audit.Log
	Projects *projects.Service
	Deploy   *deploy.Service
	Proxy    *proxy.Service
	Git      *git.Service
	Backups  *backups.Service
	Update   *update.Service
	Agent    agent.Agent
	Bus      *events.Bus
	Logger   *slog.Logger
}

// agentUpReq builds the standard up request for manual start actions.
func agentUpReq(project string) agent.ComposeUpReq {
	return agent.ComposeUpReq{Project: project, RemoveOrphans: true}
}

func (a *API) Routes(r chi.Router) {
	// Public
	r.Get("/system/health", handleHealth)
	r.Get("/auth/status", a.handleAuthStatus)
	r.Post("/auth/setup", a.handleSetup)
	r.Post("/auth/login", a.handleLogin)
	r.Post("/auth/logout", a.handleLogout)
	r.Post("/webhooks/{provider}/{project}", a.handleWebhook)

	// Authenticated
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/auth/me", a.handleMe)
		r.Get("/events", a.handleGlobalEvents)
		r.Get("/proxy/status", a.handleProxyStatus)
		r.Get("/system/metrics", a.handleSystemMetrics)
		r.Route("/projects", a.projectRoutes)
		r.Route("/git", a.gitRoutes)
		r.Route("/templates", a.templateRoutes)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole("admin"))
			r.Get("/system/backups/s3", a.handleGetS3Config)
			r.Put("/system/backups/s3", a.handleSetS3Config)
			r.Get("/system/update", a.handleCheckUpdate)
			r.Post("/system/update", a.handleApplyUpdate)
		})
	})
}
