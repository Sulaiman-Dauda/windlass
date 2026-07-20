// Package config loads platform configuration from environment variables
// with sensible defaults. Windlass is configured entirely through
// WINDLASS_* variables so it stays trivial to run under systemd or Docker.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

type Config struct {
	// Addr is the listen address for the HTTP server.
	Addr string
	// DataDir holds platform state: SQLite database, secret key, projects.
	DataDir string
	// ProjectsDir is where compose project directories live. Each project is
	// a plain directory a user can inspect and edit with standard tools.
	ProjectsDir string
	// LogLevel controls slog verbosity.
	LogLevel slog.Level
	// PanelUpstream is the Caddy dial target used for the Settings-managed
	// panel hostname. Override it when Caddy is not on the host network.
	PanelUpstream string
	// CaddyAdmin is the Caddy admin API base URL.
	CaddyAdmin string
	// UpdateRepo is the GitHub repository ("owner/name") self-update checks
	// for releases.
	UpdateRepo string
	// UpdateToken optionally authenticates release checks and downloads,
	// required when UpdateRepo is private.
	UpdateToken string
}

func Load() (Config, error) {
	cfg := Config{
		Addr:          envOr("WINDLASS_ADDR", ":8080"),
		DataDir:       envOr("WINDLASS_DATA", defaultDataDir()),
		LogLevel:      slog.LevelInfo,
		PanelUpstream: envOr("WINDLASS_PANEL_UPSTREAM", "127.0.0.1:8080"),
		CaddyAdmin:    envOr("WINDLASS_CADDY_ADMIN", "http://127.0.0.1:2019"),
		UpdateRepo:    envOr("WINDLASS_UPDATE_REPO", "windlass-dev/windlass"),
		UpdateToken:   os.Getenv("WINDLASS_UPDATE_TOKEN"),
	}

	switch os.Getenv("WINDLASS_LOG_LEVEL") {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	case "", "info":
	default:
		return cfg, fmt.Errorf("invalid WINDLASS_LOG_LEVEL %q", os.Getenv("WINDLASS_LOG_LEVEL"))
	}

	cfg.ProjectsDir = envOr("WINDLASS_PROJECTS", filepath.Join(cfg.DataDir, "projects"))
	return cfg, nil
}

func defaultDataDir() string {
	if os.PathSeparator == '\\' { // Windows dev machine
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "windlass")
	}
	return "/var/lib/windlass"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
