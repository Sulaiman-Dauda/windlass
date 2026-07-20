// Package api contains the /api/v1 HTTP handlers. Handlers stay thin:
// validate the request, call a service, write a response.
package api

import (
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/audit"
	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/backups"
	"github.com/windlass-dev/windlass/internal/deploy"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/git"
	"github.com/windlass-dev/windlass/internal/plugins"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/proxy"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store/db"
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
	Plugins  *plugins.Service
	Agent    agent.Agent
	Bus      *events.Bus
	Queries  *db.Queries
	Box      *secrets.Box
	Logger   *slog.Logger

	authLimiter *authRateLimiter
}

// agentUpReq builds the standard up request for manual start actions.
func agentUpReq(project string) agent.ComposeUpReq {
	return agent.ComposeUpReq{Project: project, RemoveOrphans: true}
}

func (a *API) Routes(r chi.Router) {
	// 20 credential attempts per IP per minute.
	a.authLimiter = newAuthRateLimiter(20, time.Minute)

	// Public
	r.Get("/system/health", handleHealth)
	r.Get("/openapi.yaml", handleOpenAPI)
	r.Get("/auth/status", a.handleAuthStatus)
	r.Post("/auth/setup", a.limitAuth(a.handleSetup))
	r.Post("/auth/login", a.limitAuth(a.handleLogin))
	r.Post("/auth/logout", a.handleLogout)
	r.Get("/auth/oauth/providers", a.handleOAuthProviders)
	r.Get("/auth/oauth/{provider}/start", a.handleOAuthStart)
	r.Get("/auth/oauth/{provider}/callback", a.handleOAuthCallback)
	r.Post("/webhooks/github-app", a.handleAppWebhook)
	r.Post("/webhooks/{provider}/{project}", a.handleWebhook)

	// Authenticated
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/auth/me", a.handleMe)
		r.Post("/auth/totp/setup", a.handleTOTPSetup)
		r.Post("/auth/totp/verify", a.handleTOTPVerify)
		r.Post("/auth/totp/disable", a.handleTOTPDisable)
		r.Get("/events", a.handleGlobalEvents)
		r.Get("/proxy/status", a.handleProxyStatus)
		r.Get("/system/metrics", a.handleSystemMetrics)
		// GitHub redirects the admin's browser here after authorizing the
		// repo-scope connect flow; the session cookie authenticates it.
		r.With(auth.RequireRole("admin")).
			Get("/auth/oauth/github/callback/git", a.handleGitConnectCallback)
		r.Route("/projects", a.projectRoutes)
		r.Route("/git", a.gitRoutes)
		r.Route("/templates", a.templateRoutes)
		r.Route("/plugins", a.pluginRoutes)
		r.Route("/users", a.userRoutes)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole("admin"))
			r.Get("/system/backups/s3", a.handleGetS3Config)
			r.Put("/system/backups/s3", a.handleSetS3Config)
			r.Get("/system/update", a.handleCheckUpdate)
			r.Post("/system/update", a.handleApplyUpdate)
			r.Get("/system/docker/images", a.handleImageDiskUsage)
			r.Post("/system/docker/images/prune", a.handlePruneImages)
			r.Get("/system/panel-domain", a.handleGetPanelDomain)
			r.Put("/system/panel-domain", a.handleSetPanelDomain)
			r.Put("/system/oauth/{provider}", a.handleSetOAuthConfig)
			r.Get("/system/github-app", a.handleGitHubAppStatus)
			r.Get("/system/github-app/create", a.handleGitHubAppCreate)
			r.Get("/system/github-app/callback", a.handleGitHubAppCallback)
		})
	})
}
