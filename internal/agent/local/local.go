// Package local is the in-process implementation of agent.Agent — the only
// package in Windlass allowed to touch Docker, Caddy, or the projects
// filesystem directly (enforced by depguard). When the node agent is split
// into its own binary, this package becomes its core and a gRPC/mTLS client
// takes its place in the control plane.
package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/moby/moby/client"

	"github.com/windlass-dev/windlass/internal/agent"
)

type Config struct {
	// ProjectsDir is the root under which every compose project lives.
	ProjectsDir string
	// CaddyAdmin is the Caddy admin API base URL.
	CaddyAdmin string
	// PanelUpstream is Caddy's dial target for the Windlass UI/API.
	PanelUpstream string
	// DockerBin is the docker CLI used for compose operations.
	DockerBin string
}

type Local struct {
	cfg Config

	mu  sync.Mutex
	cli *client.Client // lazily initialized so startup works without Docker
}

var _ agent.Agent = (*Local)(nil)

func New(cfg Config) (*Local, error) {
	if cfg.ProjectsDir == "" {
		return nil, fmt.Errorf("projects dir required")
	}
	if cfg.CaddyAdmin == "" {
		cfg.CaddyAdmin = "http://127.0.0.1:2019"
	}
	if cfg.PanelUpstream == "" {
		cfg.PanelUpstream = "127.0.0.1:8080"
	}
	if cfg.DockerBin == "" {
		cfg.DockerBin = "docker"
	}
	if err := os.MkdirAll(cfg.ProjectsDir, 0o750); err != nil {
		return nil, fmt.Errorf("create projects dir: %w", err)
	}
	return &Local{cfg: cfg}, nil
}

// docker returns the SDK client, connecting on first use.
func (l *Local) docker() (*client.Client, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cli != nil {
		return l.cli, nil
	}
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("connect docker: %w", err)
	}
	l.cli = cli
	return cli, nil
}

func (l *Local) Compose() agent.ComposeAgent { return composeLocal{l} }
func (l *Local) Docker() agent.DockerAgent   { return dockerLocal{l} }
func (l *Local) Proxy() agent.ProxyAgent     { return proxyLocal{l} }
func (l *Local) FS() agent.FSAgent           { return fsLocal{l} }
func (l *Local) Exec() agent.ExecAgent       { return execLocal{l} }
func (l *Local) Host() agent.HostAgent       { return hostLocal{l} }

func (l *Local) Ping(ctx context.Context) (agent.NodeInfo, error) {
	info := agent.NodeInfo{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if h, err := os.Hostname(); err == nil {
		info.Hostname = h
	}

	if cli, err := l.docker(); err == nil {
		if v, err := cli.ServerVersion(ctx, client.ServerVersionOptions{}); err == nil {
			info.DockerVersion = v.Version
		}
	}

	if out, err := exec.CommandContext(ctx, l.cfg.DockerBin, "compose", "version", "--short").Output(); err == nil {
		info.ComposeVersion = strings.TrimSpace(string(out))
	}

	if available, version := caddyPing(ctx, l.cfg.CaddyAdmin); available {
		info.CaddyVersion = version
	}
	return info, nil
}
