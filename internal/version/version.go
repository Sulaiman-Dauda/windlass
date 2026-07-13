// Package version exposes build-time version information injected via
// -ldflags "-X github.com/windlass-dev/windlass/internal/version.Version=...".
package version

var (
	Version = "dev"
	Commit  = "unknown"
)
