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
	"sync"
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
	r.Use(trustedRealIP(trustedProxies, logger))
	r.Use(securityHeaders(inlineScriptHashes(dist)))
	r.Use(requestLogger(logger))
	r.Use(middleware.Recoverer)
	// Refuse state-changing requests a browser sends from another origin.
	// SameSite=Lax alone lets a sibling subdomain through, and Windlass
	// serves its users' apps on exactly those. Webhooks and API clients send
	// neither Sec-Fetch-Site nor Origin, so they pass.
	r.Use(http.NewCrossOriginProtection().Handler)
	r.Use(auth.Middleware(a.Auth))

	r.Route("/api/v1", a.Routes)
	// Any other /api path is a client's mistake, not a panel page.
	r.HandleFunc("/api", api.NotFound)
	r.HandleFunc("/api/*", api.NotFound)
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

func trustedRealIP(trusted []*net.IPNet, logger *slog.Logger) func(http.Handler) http.Handler {
	ignored := &ignoredProxies{logger: logger, seen: map[string]struct{}{}}
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
			peerIP := net.ParseIP(strings.Trim(peer, "[]"))
			trustedPeer := contains(peerIP)
			// Decided here, on the address that actually connected: the
			// forwarding headers below may rewrite RemoteAddr.
			r = api.SetForwardedProto(r, trustedPeer && forwardedHTTPS(r.Header))
			if !trustedPeer {
				if len(r.Header.Values("X-Forwarded-Proto")) > 0 || len(r.Header.Values("X-Forwarded-For")) > 0 {
					ignored.warn(peerIP)
				}
				next.ServeHTTP(w, r)
				return
			}

			// Walk right-to-left: a trusted proxy appends the address it saw,
			// while any client-supplied spoof is farther left. Every header
			// line counts, in order: a proxy may add a line of its own rather
			// than extend the client's, which then stays first.
			//
			// The walk ends at the first address that is not a trusted proxy:
			// that is the client. It also ends at an entry that is not an
			// address at all, because everything left of it is unverified;
			// stepping over it would land on what the client wrote. If every
			// entry is a trusted proxy, the farthest one is the client.
			if lines := r.Header.Values("X-Forwarded-For"); len(lines) > 0 {
				forwarded := strings.Split(strings.Join(lines, ","), ",")
				for i := len(forwarded) - 1; i >= 0; i-- {
					ip := forwardedAddress(forwarded[i])
					if ip == nil {
						break
					}
					r.RemoteAddr = ip.String()
					if !contains(ip) {
						break
					}
				}
				next.ServeHTTP(w, r)
				return
			}
			// X-Real-IP is for a proxy that sends no X-Forwarded-For. Beside
			// one it is ignored: Caddy passes a client's X-Real-IP through
			// untouched. The last line counts, for the reason above.
			if lines := r.Header.Values("X-Real-IP"); len(lines) > 0 {
				if ip := forwardedAddress(lines[len(lines)-1]); ip != nil {
					r.RemoteAddr = ip.String()
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// maxIgnoredProxies bounds how many addresses ignoredProxies remembers, and
// so how many warnings one run can log.
const maxIgnoredProxies = 16

// ignoredProxies logs, once for each address, that something on a private
// network is forwarding requests without being in WINDLASS_TRUSTED_PROXIES.
// That is a reverse proxy nobody told Windlass about far more often than an
// attack, and the result is otherwise silent: no Secure cookies, http:// in
// callback URLs, every client recorded at the proxy's address.
//
// Public addresses are left out. Anyone on the internet can send these
// headers, and a scanner would use up the warnings before the real proxy
// showed.
type ignoredProxies struct {
	logger *slog.Logger
	mu     sync.Mutex
	seen   map[string]struct{}
}

func (p *ignoredProxies) warn(peer net.IP) {
	private := peer != nil && (peer.IsPrivate() || peer.IsLoopback() || peer.IsLinkLocalUnicast())
	if !private {
		return
	}
	addr := peer.String()
	p.mu.Lock()
	_, known := p.seen[addr]
	if !known && len(p.seen) < maxIgnoredProxies {
		p.seen[addr] = struct{}{}
	} else {
		known = true
	}
	p.mu.Unlock()
	if known {
		return
	}
	p.logger.Warn("forwarding headers ignored: this address is not in WINDLASS_TRUSTED_PROXIES. "+
		"If it is your reverse proxy, add it, or Windlass treats every request as plain HTTP "+
		"and records the proxy's address as the client's",
		"address", addr)
}

// forwardedAddress reads one X-Forwarded-For entry. Some proxies write the
// address with a port or in brackets. Anything else, "unknown" or an IPv6
// zone included, is nil.
func forwardedAddress(entry string) net.IP {
	entry = strings.TrimSpace(entry)
	if ip := net.ParseIP(entry); ip != nil {
		return ip
	}
	if host, _, err := net.SplitHostPort(entry); err == nil {
		return net.ParseIP(host)
	}
	if strings.HasPrefix(entry, "[") && strings.HasSuffix(entry, "]") {
		return net.ParseIP(entry[1 : len(entry)-1])
	}
	return nil
}

// forwardedHTTPS reports whether the nearest proxy said the request reached
// it over HTTPS. Only the last X-Forwarded-Proto value counts, the same
// right-to-left rule as X-Forwarded-For: a proxy that appends its own value
// instead of replacing the header leaves a client-supplied one farther left.
func forwardedHTTPS(h http.Header) bool {
	values := h.Values("X-Forwarded-Proto")
	if len(values) == 0 {
		return false
	}
	last := values[len(values)-1]
	if i := strings.LastIndexByte(last, ','); i >= 0 {
		last = last[i+1:]
	}
	return strings.EqualFold(strings.TrimSpace(last), "https")
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
			if api.ForwardedProto(r) {
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
