package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/windlass-dev/windlass/internal/agent"
)

// The files the installer writes. install.sh keeps the same paths and the
// same admin-only Caddyfile; change them together.
const (
	caddyfilePath      = "etc/caddy/Caddyfile"
	caddyDropInPath    = "etc/systemd/system/caddy.service.d/windlass-resume.conf"
	systemdRunDir      = "run/systemd/system"
	adminOnlyCaddyfile = "{admin127.0.0.1:2019}"
)

// ResumeState reads the Caddyfile and the drop-in. The service runs with
// ProtectSystem=full, which leaves /etc readable, so no D-Bus or systemctl
// call is needed. Any file it cannot read counts as not ours: a panel in a
// container sees no /etc/caddy and must not ask for a fix it cannot apply.
func (p proxyLocal) ResumeState(ctx context.Context) (agent.CaddyResume, error) {
	return caddyResumeState("/"), nil
}

func caddyResumeState(root string) agent.CaddyResume {
	if fi, err := os.Stat(filepath.Join(root, systemdRunDir)); err != nil || !fi.IsDir() {
		return agent.ResumeNotOurs
	}
	raw, err := os.ReadFile(filepath.Join(root, caddyfilePath))
	if err != nil || squeeze(string(raw)) != adminOnlyCaddyfile {
		return agent.ResumeNotOurs
	}
	dropIn, err := os.ReadFile(filepath.Join(root, caddyDropInPath))
	if err == nil && strings.Contains(string(dropIn), "--resume") {
		return agent.ResumeOn
	}
	return agent.ResumeOff
}

// squeeze drops the spaces, tabs and newlines install.sh's tr drops.
func squeeze(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' {
			return -1
		}
		return r
	}, s)
}
