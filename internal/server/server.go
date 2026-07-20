// Package server wires the chi router: API routes, middleware, and the
// embedded single-page frontend.
package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
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

	dist, err := web.Dist()
	if err != nil {
		return nil, err
	}

	r.Use(middleware.RealIP)
	r.Use(securityHeaders(inlineScriptHashes(dist)))
	r.Use(requestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Use(auth.Middleware(a.Auth))

	r.Route("/api/v1", a.Routes)
	r.NotFound(spaHandler(dist))

	return r, nil
}

var inlineScriptRE = regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>(.*?)</script>`)

// inlineScriptHashes returns 'sha256-…' sources for every inline script in
// index.html. The SPA runs one before first paint to apply the saved theme,
// and script-src 'self' alone blocks it — hashing what is actually served
// keeps the policy strict without the hash drifting when the script changes.
func inlineScriptHashes(dist fs.FS) []string {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range inlineScriptRE.FindAllSubmatch(index, -1) {
		// Tags with a src attribute load external code; 'self' covers those
		// and they carry no hashable body.
		if bytes.Contains(m[0][:len(m[0])-len(m[1])-len("</script>")], []byte("src=")) {
			continue
		}
		if len(bytes.TrimSpace(m[1])) == 0 {
			continue
		}
		sum := sha256.Sum256(m[1])
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
}

func securityHeaders(scriptHashes []string) func(http.Handler) http.Handler {
	scriptSrc := strings.Join(append([]string{"script-src 'self'"}, scriptHashes...), " ")
	csp := "default-src 'self'; base-uri 'self'; connect-src 'self'; font-src 'self' data:; " +
		"form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; " +
		scriptSrc + "; style-src 'self' 'unsafe-inline'"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
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
