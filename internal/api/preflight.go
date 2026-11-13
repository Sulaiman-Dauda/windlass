package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"syscall"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/version"
)

// preflightMinFreeBytes is the free disk below which first-run checks warn.
// Images and build cache alone can take several gigabytes on a first deploy.
const preflightMinFreeBytes = 5 << 30

// preflightCheck is one line of the first-run checks (F10, #117).
type preflightCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"` // ok | warn | fail
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

type preflightResponse struct {
	Version string           `json:"version"`
	Checks  []preflightCheck `json:"checks"`
}

// handlePreflight reports what a fresh server is missing. Before the claim
// the setup token (X-Setup-Token) opens it, since reading the token proves
// host access; afterwards only an admin may read it. It changes nothing.
func (a *API) handlePreflight(w http.ResponseWriter, r *http.Request) {
	if !a.preflightLimiter.allow(remoteIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many checks; try again in a minute")
		return
	}
	user, signedIn := auth.UserFrom(r.Context())
	allowed := a.Auth.SetupTokenValid(r.Context(), r.Header.Get("X-Setup-Token")) ||
		(signedIn && user.Role == "admin")
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "setup token or admin sign-in required")
		return
	}
	writeJSON(w, http.StatusOK, preflightResponse{Version: version.Version, Checks: a.preflight(r)})
}

func (a *API) preflight(r *http.Request) []preflightCheck {
	ctx := r.Context()
	var checks []preflightCheck
	node, _ := a.Agent.Ping(ctx)

	// Docker is reached through the restricted socket proxy, so no version
	// means the proxy or Docker behind it is down.
	if node.DockerVersion == "" {
		checks = append(checks, preflightCheck{ID: "docker", Status: "fail", Title: "Docker is not reachable",
			Detail: "Windlass reaches Docker through its restricted socket proxy (DOCKER_HOST). Check that Docker and the proxy are running."})
	} else {
		checks = append(checks, preflightCheck{ID: "docker", Status: "ok", Title: "Docker " + node.DockerVersion,
			Detail: "Reached through the socket proxy."})
	}
	if node.ComposeVersion == "" {
		checks = append(checks, preflightCheck{ID: "compose", Status: "fail", Title: "Docker Compose is missing",
			Detail: "Deploys run docker compose. Install the Compose plugin (docker-compose-plugin)."})
	} else {
		checks = append(checks, preflightCheck{ID: "compose", Status: "ok", Title: "Docker Compose " + node.ComposeVersion})
	}

	if node.CaddyVersion == "" {
		checks = append(checks, preflightCheck{ID: "caddy", Status: "warn", Title: "Caddy is not answering",
			Detail: "Without Caddy there are no domains or HTTPS. Its admin API should answer on localhost:2019."})
	} else {
		checks = append(checks, preflightCheck{ID: "caddy", Status: "ok", Title: "Caddy " + node.CaddyVersion})
		checks = append(checks, a.listenChecks(ctx)...)
	}

	if isTLS(r) {
		checks = append(checks, preflightCheck{ID: "panel_https", Status: "ok", Title: "Panel opened over HTTPS"})
	} else if host := requestHost(r); host != "localhost" && !isLoopback(host) {
		checks = append(checks, preflightCheck{ID: "panel_https", Status: "warn", Title: "Panel opened over plain HTTP",
			Detail: "Your password crosses the network unencrypted. Use an SSH tunnel now, then set a panel domain in Settings and close port 8080."})
	} else {
		checks = append(checks, preflightCheck{ID: "panel_https", Status: "ok", Title: "Panel opened on this machine or through a tunnel"})
	}

	if m, err := a.Agent.Host().Metrics(ctx); err == nil && m.DiskTotal > 0 {
		free := m.DiskTotal - min(m.DiskUsed, m.DiskTotal)
		c := preflightCheck{ID: "disk", Status: "ok", Title: fmt.Sprintf("%s free of %s", gib(free), gib(m.DiskTotal))}
		if free < preflightMinFreeBytes {
			c.Status = "warn"
			c.Detail = "Images and build cache need room. Under 5 GiB free, the first deploys may fail."
		}
		checks = append(checks, c)
	}
	return checks
}

// listenChecks asks whether anything answers on this server's ports 80 and
// 443. It cannot see a firewall in front of them, which the detail says.
func (a *API) listenChecks(ctx context.Context) []preflightCheck {
	ports := []struct{ id, url, title string }{
		{"port_80", "http://127.0.0.1:80/", "port 80"},
		{"port_443", "https://127.0.0.1:443/", "port 443"},
	}
	out := make([]preflightCheck, len(ports))
	var wg sync.WaitGroup
	for i, p := range ports {
		wg.Go(func() {
			_, err := a.Agent.Host().HTTPCheck(ctx, agent.HTTPCheckReq{URL: p.url, Timeout: 3})
			// Any answer, even a TLS error for a bare IP, means Caddy listens.
			if err != nil && errors.Is(err, syscall.ECONNREFUSED) {
				out[i] = preflightCheck{ID: p.id, Status: "warn", Title: "Nothing listens on " + p.title,
					Detail: "Caddy must bind ports 80 and 443 for certificates. Check its config and journalctl -u caddy."}
				return
			}
			out[i] = preflightCheck{ID: p.id, Status: "ok", Title: "Caddy listens on " + p.title,
				Detail: "A firewall on this server or at your provider can still block it from outside. See the install guide."}
		})
	}
	wg.Wait()
	return out
}

// requestHost is the Host header without its port.
func requestHost(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.Host); err == nil {
		return host
	}
	return r.Host
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func gib(b uint64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }
