// Package server wires the chi router: API routes, middleware, and the
// embedded single-page frontend.
package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
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

	trustedProxies, err := parseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}
	r.Use(trustedRealIP(trustedProxies))
	r.Use(securityHeaders(inlineScriptHashes(dist)))
	r.Use(requestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Use(auth.Middleware(a.Auth))

	r.Route("/api/v1", a.Routes)
	r.NotFound(spaHandler(dist))

	return r, nil
}

func parseTrustedProxies(value string) ([]*net.IPNet, error) {
	if strings.TrimSpace(value) == "" {
		value = "127.0.0.0/8,::1/128"
	}
	var out []*net.IPNet
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if ip := net.ParseIP(raw); ip != nil {
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q", raw)
		}
		out = append(out, network)
	}
	return out, nil
}

func trustedRealIP(trusted []*net.IPNet) func(http.Handler) http.Handler {
	contains := func(ip net.IP) bool {
		for _, network := range trusted {
			if network.Contains(ip) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer := r.RemoteAddr
			if host, _, err := net.SplitHostPort(peer); err == nil {
				peer = host
			}
			if !contains(net.ParseIP(strings.Trim(peer, "[]"))) {
				next.ServeHTTP(w, r)
				return
			}

			// Walk right-to-left: a trusted proxy appends the address it saw,
			// while any client-supplied spoof is farther left.
			forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
			for i := len(forwarded) - 1; i >= 0; i-- {
				ip := net.ParseIP(strings.TrimSpace(forwarded[i]))
				if ip != nil && !contains(ip) {
					r.RemoteAddr = ip.String()
					next.ServeHTTP(w, r)
					return
				}
			}
			if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
				r.RemoteAddr = ip.String()
			}
			next.ServeHTTP(w, r)
		})
	}
}

var inlineScriptRE = regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>(.*?)</script>`)

// inlineScriptHashes returns 'sha256-…' sources for every inline script in
// index.html. The SPA runs one before first paint to apply the saved theme,
// and script-src 'self' alone blocks it, hashing what is actually served
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
				if strings.HasPrefix(path, "assets/") {
					// Vite fingerprints production assets in their filenames, so
					// they can be retained forever without serving stale code.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
			// A stale index may request an asset removed by a newer release.
			// Do not answer with index.html under a JavaScript or CSS URL.
			if strings.HasPrefix(path, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		// Client-side route: serve the SPA entrypoint.
		// It must be revalidated after every binary update so the browser
		// discovers the new fingerprinted asset names immediately.
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
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
