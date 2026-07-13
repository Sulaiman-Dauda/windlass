package local

import (
	"context"
	"net/http"
	"time"
)

type execLocal struct{ l *Local }

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
