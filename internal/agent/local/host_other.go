//go:build !linux

package local

import (
	"context"
	"errors"

	"github.com/windlass-dev/windlass/internal/agent"
)

// Non-Linux hosts (the Windows dev machine) return empty metrics; the
// product targets Linux servers.
func readHostMetrics(ctx context.Context, diskPath string) (agent.HostMetrics, error) {
	return agent.HostMetrics{}, nil
}

// freeBytes is unknown off Linux; callers treat the error as "cannot tell".
func freeBytes(path string) (uint64, error) {
	return 0, errors.ErrUnsupported
}
