package local

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

func TestCaddyResumeState(t *testing.T) {
	write := func(root, rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The exact file install.sh writes, tab and all.
	const ours = "{\n\tadmin 127.0.0.1:2019\n}\n"
	cases := []struct {
		name      string
		systemd   bool
		caddyfile string
		dropIn    string
		want      agent.CaddyResume
	}{
		{"no systemd", false, ours, "", agent.ResumeNotOurs},
		{"no caddyfile", true, "", "", agent.ResumeNotOurs},
		{"user caddyfile", true, "example.com {\n\treverse_proxy :3000\n}\n", "", agent.ResumeNotOurs},
		{"ours, no drop-in", true, ours, "", agent.ResumeOff},
		{"ours, drop-in without resume", true, ours, "[Service]\n", agent.ResumeOff},
		{"ours, drop-in", true, ours, "[Service]\nExecStart=\nExecStart=/usr/bin/caddy run --environ --resume --config /etc/caddy/Caddyfile\n", agent.ResumeOn},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if c.systemd {
				if err := os.MkdirAll(filepath.Join(root, systemdRunDir), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if c.caddyfile != "" {
				write(root, caddyfilePath, c.caddyfile)
			}
			if c.dropIn != "" {
				write(root, caddyDropInPath, c.dropIn)
			}
			if got := caddyResumeState(root); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}
