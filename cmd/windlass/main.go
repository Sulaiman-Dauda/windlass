package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/windlass-dev/windlass/internal/agent/local"
	"github.com/windlass-dev/windlass/internal/api"
	"github.com/windlass-dev/windlass/internal/audit"
	"github.com/windlass-dev/windlass/internal/auth"
	"github.com/windlass-dev/windlass/internal/backups"
	"github.com/windlass-dev/windlass/internal/config"
	"github.com/windlass-dev/windlass/internal/deploy"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/git"
	"github.com/windlass-dev/windlass/internal/jobs"
	"github.com/windlass-dev/windlass/internal/plugins"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/proxy"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/server"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/internal/update"
	"github.com/windlass-dev/windlass/internal/version"
	"github.com/windlass-dev/windlass/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "windlass:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	// Storage: SQLite metadata DB + key files, all under DataDir/data.
	dataDir := filepath.Join(cfg.DataDir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return err
	}
	sqlDB, err := store.Open(filepath.Join(dataDir, "windlass.db"))
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if err := store.Migrate(sqlDB, migrations.FS); err != nil {
		return err
	}
	queries := db.New(sqlDB)

	sessionKey, err := secrets.LoadKey(filepath.Join(dataDir, "session.key"))
	if err != nil {
		return err
	}
	box, err := secrets.Load(filepath.Join(dataDir, "secret.key"))
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	authSvc, err := auth.NewService(ctx, queries, sessionKey, box, logger)
	if err != nil {
		return err
	}

	// The agent is the single privileged boundary (principle 11).
	ag, err := local.New(local.Config{ProjectsDir: cfg.ProjectsDir, CaddyAdmin: cfg.CaddyAdmin,
		PanelUpstream: cfg.PanelUpstream})
	if err != nil {
		return err
	}

	bus := events.NewBus()
	projectSvc := projects.New(queries, ag, box, bus, logger)
	if err := projectSvc.Reconcile(ctx); err != nil {
		return fmt.Errorf("reconcile projects: %w", err)
	}

	gitSvc := git.New(queries, box, logger)
	runner := jobs.NewRunner(queries, logger)
	deploySvc := deploy.New(queries, ag, projectSvc, gitSvc, runner, bus, logger)
	go func() {
		if err := runner.Run(ctx); err != nil {
			logger.Error("job runner stopped", "error", err)
		}
	}()

	proxySvc := proxy.New(queries, ag, projectSvc, bus, logger)
	go proxySvc.Run(ctx)

	backupSvc := backups.New(queries, ag, projectSvc, box, bus, logger)
	go backupSvc.RunScheduler(ctx)

	update.Repo = cfg.UpdateRepo
	update.Token = cfg.UpdateToken
	updateSvc := update.New(logger, cfg.DataDir, stop)

	pluginSvc := plugins.New(queries, cfg.DataDir, logger)
	pluginSvc.StartEnabled(ctx)
	defer pluginSvc.StopAll()

	a := &api.API{
		Auth:     authSvc,
		Audit:    audit.New(queries, logger),
		Projects: projectSvc,
		Deploy:   deploySvc,
		Proxy:    proxySvc,
		Git:      gitSvc,
		Backups:  backupSvc,
		Update:   updateSvc,
		Plugins:  pluginSvc,
		Agent:    ag,
		Bus:      bus,
		Queries:  queries,
		Box:      box,
		Logger:   logger,
	}

	handler, err := server.New(cfg, logger, a)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("windlass starting", "addr", cfg.Addr, "version", version.Version)
		server.NotifySystemd("READY=1")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
