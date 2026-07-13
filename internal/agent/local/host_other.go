//go:build !linux

package local

import (
	"context"

	"github.com/windlass-dev/windlass/internal/agent"
)

// Non-Linux hosts (the Windows dev machine) return empty metrics; the
// product targets Linux servers.
func readHostMetrics(ctx context.Context, diskPath string) (agent.HostMetrics, error) {
	return agent.HostMetrics{}, nil
}
