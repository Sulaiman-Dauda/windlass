package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

// Windlass owns exactly one object in Caddy's config: a route tagged
// "@id": "windlass_routes" whose subroute holds one route per domain.
// It is addressed only via targeted /id/ operations — never POST /load —
// so hand-written Caddy configuration is never touched (plan risk #2).
const routesID = "windlass_routes"

type proxyLocal struct{ l *Local }

func (p proxyLocal) client() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

func (p proxyLocal) Available(ctx context.Context) (agent.ProxyInfo, error) {
	available, version := caddyPing(ctx, p.l.cfg.CaddyAdmin)
	return agent.ProxyInfo{Available: available, Version: version}, nil
}

// caddyRoute mirrors the subset of Caddy's route JSON Windlass emits.
type caddyRoute struct {
	ID       string        `json:"@id,omitempty"`
	Match    []caddyMatch  `json:"match,omitempty"`
	Handle   []caddyHandle `json:"handle,omitempty"`
	Terminal bool          `json:"terminal,omitempty"`
}

type caddyMatch struct {
	Host []string `json:"host,omitempty"`
}

type caddyHandle struct {
	Handler   string          `json:"handler"`
	Routes    []caddyRoute    `json:"routes,omitempty"`    // subroute
	Upstreams []caddyUpstream `json:"upstreams,omitempty"` // reverse_proxy
}

type caddyUpstream struct {
	Dial string `json:"dial"`
}

// buildRoutesObject renders the full Windlass-owned route subtree. Pure so
// it can be golden-tested without Caddy.
func buildRoutesObject(routes []agent.Route) caddyRoute {
	inner := make([]caddyRoute, 0, len(routes))
	for _, r := range routes {
		inner = append(inner, caddyRoute{
			ID:    r.ID,
			Match: []caddyMatch{{Host: []string{r.Hostname}}},
			Handle: []caddyHandle{{
				Handler:   "reverse_proxy",
				Upstreams: []caddyUpstream{{Dial: r.Upstream}},
			}},
			Terminal: true,
		})
	}
	return caddyRoute{
		ID:     routesID,
		Handle: []caddyHandle{{Handler: "subroute", Routes: inner}},
	}
}

func (p proxyLocal) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.l.cfg.CaddyAdmin+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return p.client().Do(req)
}

// ApplyRoutes replaces the Windlass-owned subtree with the desired state.
func (p proxyLocal) ApplyRoutes(ctx context.Context, routes []agent.Route) error {
	obj := buildRoutesObject(routes)

	// Fast path: the object exists — PATCH it in place (zero-downtime).
	resp, err := p.do(ctx, http.MethodPatch, "/id/"+routesID, obj)
	if err != nil {
		return fmt.Errorf("caddy admin unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	// First run: install the object into a server.
	return p.install(ctx, obj)
}

// install places the Windlass route object at the front of a suitable HTTP
// server's route list, creating the server if none exists.
func (p proxyLocal) install(ctx context.Context, obj caddyRoute) error {
	resp, err := p.do(ctx, http.MethodGet, "/config/apps/http/servers", nil)
	if err != nil {
		return fmt.Errorf("caddy admin unreachable: %w", err)
	}
	defer resp.Body.Close()

	servers := map[string]struct {
		Listen []string `json:"listen"`
	}{}
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&servers); err != nil {
			servers = nil
		}
	}

	// Prefer a server already listening on :443 or :80.
	target := ""
	for name, srv := range servers {
		for _, l := range srv.Listen {
			if l == ":443" || l == ":80" {
				target = name
				break
			}
		}
		if target != "" {
			break
		}
	}

	if target == "" {
		// No usable server: create one that Caddy will run auto-HTTPS on.
		server := map[string]any{
			"listen": []string{":80", ":443"},
			"routes": []caddyRoute{obj},
		}
		// Parent objects may not exist on a fresh Caddy; build the path.
		for _, step := range []struct{ path string; body any }{
			{"/config/apps", map[string]any{}},
			{"/config/apps/http", map[string]any{}},
			{"/config/apps/http/servers", map[string]any{}},
		} {
			r, err := p.do(ctx, http.MethodGet, step.path, nil)
			if err != nil {
				return err
			}
			created := r.StatusCode != http.StatusOK
			r.Body.Close()
			if created {
				r2, err := p.do(ctx, http.MethodPut, step.path, step.body)
				if err != nil {
					return err
				}
				r2.Body.Close()
			}
		}
		r, err := p.do(ctx, http.MethodPut, "/config/apps/http/servers/windlass", server)
		if err != nil {
			return err
		}
		defer r.Body.Close()
		if r.StatusCode >= 300 {
			msg, _ := io.ReadAll(r.Body)
			return fmt.Errorf("create caddy server: %s: %s", r.Status, msg)
		}
		return nil
	}

	// Insert at index 0 so a catch-all site in the user's Caddyfile can't
	// shadow Windlass domains.
	r, err := p.do(ctx, http.MethodPut, "/config/apps/http/servers/"+target+"/routes/0", obj)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode >= 300 {
		msg, _ := io.ReadAll(r.Body)
		return fmt.Errorf("install caddy routes: %s: %s", r.Status, msg)
	}
	return nil
}

func (p proxyLocal) CurrentRoutes(ctx context.Context) ([]agent.Route, error) {
	resp, err := p.do(ctx, http.MethodGet, "/id/"+routesID, nil)
	if err != nil {
		return nil, fmt.Errorf("caddy admin unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // nothing installed yet
	}

	var obj caddyRoute
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, err
	}
	var out []agent.Route
	for _, h := range obj.Handle {
		for _, r := range h.Routes {
			route := agent.Route{ID: r.ID, TLS: true}
			if len(r.Match) > 0 && len(r.Match[0].Host) > 0 {
				route.Hostname = r.Match[0].Host[0]
			}
			for _, ih := range r.Handle {
				if len(ih.Upstreams) > 0 {
					route.Upstream = ih.Upstreams[0].Dial
				}
			}
			out = append(out, route)
		}
	}
	return out, nil
}
