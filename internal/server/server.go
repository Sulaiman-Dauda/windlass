// Package server wires the chi router: API routes, middleware, and the
// embedded single-page frontend.
package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/windlass-dev/windlass/internal/api"
	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/config"
	"github.com/windlass-dev/windlass/web"
)

func New(cfg config.Config, logger *slog.Logger, a *api.API) (http.Handler, error) {
	r := chi.NewRouter()

	r.Use(middleware.RealIP)
	r.Use(securityHeaders)
	r.Use(requestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Use(auth.Middleware(a.Auth))

	r.Route("/api/v1", a.Routes)

	dist, err := web.Dist()
	if err != nil {
		return nil, err
	}
	r.NotFound(spaHandler(dist))

	return r, nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; connect-src 'self'; font-src 'self' data:; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'")
		h.Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// spaHandler serves static assets from the embedded frontend build and falls
// back to index.html for client-side routes.
func spaHandler(dist fs.FS) http.HandlerFunc {
	fileServer := http.FileServerFS(dist)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(dist, path); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// Client-side route: serve the SPA entrypoint.
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			logger.Debug("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration", time.Since(start),
				"remote", r.RemoteAddr,
			)
		})
	}
}
