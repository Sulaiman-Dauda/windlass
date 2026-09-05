package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

// Windlass owns exactly one object in Caddy's config: a route tagged
// "@id": "windlass_routes" whose subroute holds one route per domain.
// It is addressed only via targeted /id/ operations — never POST /load —
// so hand-written Caddy configuration is never touched (plan risk #2).
const (
	routesID        = "windlass_routes"
	panelRouteID    = "windlass_panel_route"
	httpsRedirectID = "windlass_https_redirect"
)

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
	Host     []string `json:"host,omitempty"`
	Protocol string   `json:"protocol,omitempty"`
}

type caddyHandle struct {
	Handler    string              `json:"handler"`
	Routes     []caddyRoute        `json:"routes,omitempty"`      // subroute
	Upstreams  []caddyUpstream     `json:"upstreams,omitempty"`   // reverse_proxy
	StatusCode int                 `json:"status_code,omitempty"` // static_response
	Headers    map[string][]string `json:"headers,omitempty"`     // static_response
}

type caddyUpstream struct {
	Dial string `json:"dial"`
}

// buildRoutesObject renders the full Windlass-owned route subtree. Pure so
// it can be golden-tested without Caddy.
func buildRoutesObject(routes []agent.Route) caddyRoute {
	inner := make([]caddyRoute, 0, len(routes)+1)

	// Redirect plaintext HTTP to HTTPS for the TLS-enabled Windlass domains,
	// ahead of the reverse-proxy routes. It sits inside the subroute (not as a
	// sibling of windlass_routes) so ordering is deterministic on both the
	// create and graft-onto-existing-server paths, and is scoped by host so a
	// shared server (e.g. an administrator Caddyfile) keeps serving its own
	// HTTP sites. ACME is not special-cased: issuance/renewal uses TLS-ALPN-01
	// on :443, and any HTTP-01 fallback is answered by Caddy's own challenge
	// handler, which it orders ahead of application routes.
	var httpsHosts []string
	for _, r := range routes {
		if r.TLS && r.Hostname != "" {
			httpsHosts = append(httpsHosts, r.Hostname)
		}
	}
	if len(httpsHosts) > 0 {
		inner = append(inner, caddyRoute{
			ID: httpsRedirectID,
			Match: []caddyMatch{{
				Protocol: "http",
				Host:     httpsHosts,
			}},
			Handle: []caddyHandle{{
				Handler:    "static_response",
				StatusCode: 308,
				Headers:    map[string][]string{"Location": {"https://{http.request.host}{http.request.uri}"}},
			}},
			Terminal: true,
		})
	}

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
		return p.ensureTLS(ctx, routes)
	}

	// First run: install the object into a server.
	if err := p.install(ctx, obj); err != nil {
		return err
	}
	return p.ensureTLS(ctx, routes)
}

// ApplyPanelDomain owns exactly one additional Caddy route for the Windlass
// UI/API. It uses the same targeted @id operations as application routes and
// never loads or replaces the administrator's Caddy configuration.
func (p proxyLocal) ApplyPanelDomain(ctx context.Context, hostname string) error {
	if hostname == "" {
		resp, err := p.do(ctx, http.MethodDelete, "/id/"+panelRouteID, nil)
		if err != nil {
			return fmt.Errorf("remove panel domain: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode < 300 {
			return nil
		}
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remove panel domain: %s: %s", resp.Status, msg)
	}

	obj := caddyRoute{
		ID:    panelRouteID,
		Match: []caddyMatch{{Host: []string{hostname}}},
		Handle: []caddyHandle{{Handler: "reverse_proxy",
			Upstreams: []caddyUpstream{{Dial: p.l.cfg.PanelUpstream}}}},
		Terminal: true,
	}
	resp, err := p.do(ctx, http.MethodPatch, "/id/"+panelRouteID, obj)
	if err != nil {
		return fmt.Errorf("configure panel domain: %w", err)
	}
	status := resp.StatusCode
	resp.Body.Close()
	if status != http.StatusOK {
		if err := p.install(ctx, obj); err != nil {
			return err
		}
	}
	return p.ensureTLS(ctx, []agent.Route{{Hostname: hostname, TLS: true}})
}

// ensureTLS adds routed hostnames to Caddy's certificate automation list.
// Host matchers added through the JSON API are not automatically included in
// certificate management, unlike hostnames loaded through a Caddyfile. Merge
// with the existing list so user-owned certificate automation is preserved.
// Stale names are deliberately retained: removing a Windlass domain must not
// risk deleting an identically named entry owned by the server administrator.
func (p proxyLocal) ensureTLS(ctx context.Context, routes []agent.Route) error {
	if len(routes) == 0 {
		return nil
	}

	const path = "/config/apps/tls/certificates/automate"
	var names []string
	method := http.MethodPut
	resp, err := p.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return fmt.Errorf("read caddy TLS automation: %w", err)
	}
	if resp.StatusCode == http.StatusOK {
		method = http.MethodPatch
		if err := json.NewDecoder(resp.Body).Decode(&names); err != nil {
			resp.Body.Close()
			return fmt.Errorf("decode caddy TLS automation: %w", err)
		}
	} else if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest {
		resp.Body.Close()
		// Caddy reports 400 "invalid traversal path" when an intermediate
		// config object has never existed. Create only missing parents; never
		// replace user-owned TLS configuration.
		for _, parent := range []string{"/config/apps", "/config/apps/tls", "/config/apps/tls/certificates"} {
			current, parentErr := p.do(ctx, http.MethodGet, parent, nil)
			if parentErr != nil {
				return parentErr
			}
			exists := current.StatusCode == http.StatusOK
			current.Body.Close()
			if exists {
				continue
			}
			created, parentErr := p.do(ctx, http.MethodPut, parent, map[string]any{})
			if parentErr != nil {
				return parentErr
			}
			if created.StatusCode >= 300 {
				msg, _ := io.ReadAll(created.Body)
				created.Body.Close()
				return fmt.Errorf("create caddy config parent %s: %s: %s", parent, created.Status, msg)
			}
			created.Body.Close()
		}
		method = http.MethodPut
	} else {
		msg, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return fmt.Errorf("read caddy TLS automation: %s: %s", resp.Status, msg)
	}
	if resp.Body != http.NoBody {
		resp.Body.Close()
	}

	seen := make(map[string]bool, len(names)+len(routes))
	for _, name := range names {
		seen[name] = true
	}
	changed := false
	for _, route := range routes {
		if !route.TLS || route.Hostname == "" || seen[route.Hostname] {
			continue
		}
		names = append(names, route.Hostname)
		seen[route.Hostname] = true
		changed = true
	}
	if !changed {
		return nil
	}

	resp, err = p.do(ctx, method, path, names)
	if err != nil {
		return fmt.Errorf("enable caddy TLS automation: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("enable caddy TLS automation: %s: %s", resp.Status, msg)
	}
	return nil
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
		Listen                []string          `json:"listen"`
		TLSConnectionPolicies []json.RawMessage `json:"tls_connection_policies"`
	}{}
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&servers); err != nil {
			servers = nil
		}
	}

	// Prefer the standard server, then any TLS-enabled server (including a
	// non-standard port used in tests/self-hosted setups), then the first
	// existing server. Never create a parallel server when a suitable one
	// already exists.
	target := ""
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		srv := servers[name]
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
		for _, name := range names {
			if len(servers[name].TLSConnectionPolicies) > 0 {
				target = name
				break
			}
		}
	}
	if target == "" && len(names) > 0 {
		target = names[0]
	}

	if target == "" {
		// No usable server: create one that terminates TLS on :443. The domain
		// host matchers live inside the windlass_routes subroute, which Caddy's
		// automatic-HTTPS host discovery does not traverse, so Caddy never
		// attaches a TLS connection policy on its own. Without an explicit empty
		// policy (match any SNI, serve the managed cert added by ensureTLS) the
		// :443 listener stays plaintext and application HTTPS fails even though
		// the certificate was issued. The HTTP->HTTPS redirect lives inside the
		// windlass_routes subroute (buildRoutesObject) so it applies on both
		// this create path and the graft-onto-existing-server path.
		server := map[string]any{
			"listen":                  []string{":80", ":443"},
			"routes":                  []caddyRoute{obj},
			"tls_connection_policies": []map[string]any{{}},
		}
		// Parent objects may not exist on a fresh Caddy; build the path.
		for _, step := range []struct {
			path string
			body any
		}{
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
	// shadow Windlass domains. A server with no routes key at all has no array
	// to insert into, and Caddy rejects the index rather than creating one, so
	// that case writes the whole array instead.
	base := "/config/apps/http/servers/" + target + "/routes"
	state, err := p.routesState(ctx, target)
	if err != nil {
		return err
	}
	method, path := http.MethodPut, base+"/0"
	var body any = obj
	switch state {
	case routesAbsent:
		// No array to index into, so write the whole array.
		path, body = base, []any{obj}
	case routesEmpty:
		// Caddy before 2.11 rejects index 0 on an empty array, and PUT on the
		// array itself is a conflict because the key exists. Append: with no
		// other routes present there is no ordering to get wrong.
		method, path = http.MethodPost, base
	}
	r, err := p.do(ctx, method, path, body)
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

// Caddy's admin API accepts a different call for each of these, and which
// calls work has changed between releases, so they are distinguished rather
// than guessed at. See installRoutes for the matrix.
type routesShape int

const (
	routesAbsent routesShape = iota
	routesEmpty
	routesPopulated
)

// routesState reports whether the server's routes array is missing, present but
// empty, or already holds routes. Caddy returns a JSON null for a key that is
// not set.
func (p proxyLocal) routesState(ctx context.Context, target string) (routesShape, error) {
	r, err := p.do(ctx, http.MethodGet, "/config/apps/http/servers/"+target+"/routes", nil)
	if err != nil {
		return routesAbsent, err
	}
	defer r.Body.Close()
	if r.StatusCode >= 300 {
		return routesAbsent, nil
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return routesAbsent, err
	}
	var routes []json.RawMessage
	if err := json.Unmarshal(raw, &routes); err != nil {
		// null, or anything else that is not an array: treat as absent so the
		// array gets written rather than indexed into.
		return routesAbsent, nil
	}
	if routes == nil {
		return routesAbsent, nil
	}
	if len(routes) == 0 {
		return routesEmpty, nil
	}
	return routesPopulated, nil
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
