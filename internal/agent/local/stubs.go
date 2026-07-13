package local

import (
	"context"
	"net/http"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
)

// Proxy lands in M6, Exec in M8 (see docs/plan.md). The stubs keep the
// Agent contract complete so services can be written and unit-tested
// against the fake in the meantime.

type proxyLocal struct{ l *Local }

func (p proxyLocal) Available(ctx context.Context) (agent.ProxyInfo, error) {
	available, version := caddyPing(ctx, p.l.cfg.CaddyAdmin)
	return agent.ProxyInfo{Available: available, Version: version}, nil
}
func (p proxyLocal) ApplyRoutes(ctx context.Context, routes []agent.Route) error {
	return errNotImplemented("proxy apply")
}
func (p proxyLocal) CurrentRoutes(ctx context.Context) ([]agent.Route, error) {
	return nil, errNotImplemented("proxy current")
}

type execLocal struct{ l *Local }

func (e execLocal) Start(ctx context.Context, req agent.ExecReq) (agent.ExecSession, error) {
	return nil, errNotImplemented("exec")
}

// caddyPing reports whether the Caddy admin API answers at base.
func caddyPing(ctx context.Context, base string) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/config/", nil)
	if err != nil {
		return false, ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, ""
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, ""
	}
	if server := resp.Header.Get("Server"); server != "" {
		return true, server
	}
	return true, "reachable"
}
