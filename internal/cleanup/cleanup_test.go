package cleanup

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/agent/fake"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

type env struct {
	svc    *Service
	agent  *fake.Fake
	sqlDB  *sql.DB
	mu     sync.Mutex
	audits []map[string]any
}

func newEnv(t *testing.T) *env {
	t.Helper()
	sqlDB, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := store.Migrate(sqlDB, migrations.FS); err != nil {
		t.Fatal(err)
	}
	e := &env{agent: fake.New(), sqlDB: sqlDB}
	e.svc = New(context.Background(), db.New(sqlDB), e.agent.Docker(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(_ context.Context, action string, detail any) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if action == "docker.cleanup" {
				e.audits = append(e.audits, detail.(map[string]any))
			}
		})
	return e
}

func (e *env) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := e.sqlDB.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsDefaultAndSave(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if got := e.svc.Settings(ctx); got != Defaults {
		t.Fatalf("unsaved settings = %+v, want defaults", got)
	}
	if !Defaults.Auto || Defaults.ImageDays != 7 || Defaults.KeepDeployments != 5 || Defaults.BuildCacheGB != 10 {
		t.Fatalf("defaults changed: %+v", Defaults)
	}
	for _, bad := range []Settings{
		{ImageDays: 0, KeepDeployments: 5},
		{ImageDays: 7, KeepDeployments: 0},
		{ImageDays: 7, KeepDeployments: 5, BuildCacheGB: -1},
		{ImageDays: MaxImageDays + 1, KeepDeployments: 5},
	} {
		if err := e.svc.SaveSettings(ctx, bad); err == nil {
			t.Errorf("saved out-of-range %+v", bad)
		}
	}
	want := Settings{Auto: false, ImageDays: 30, KeepDeployments: 2, BuildCacheGB: 0}
	if err := e.svc.SaveSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Settings(ctx); got != want {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
	// A stored value that no longer validates falls back rather than
	// running with it.
	e.exec(t, `UPDATE settings SET value = '{"image_days":0}' WHERE key = ?`, SettingsKey)
	if got := e.svc.Settings(ctx); got != Defaults {
		t.Fatalf("invalid stored settings = %+v, want defaults", got)
	}
}

func TestRunUsesSettingsAndKeepsRecentDeployments(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.exec(t, `INSERT INTO projects (id, name) VALUES (1, 'shop')`)
	e.exec(t, `INSERT INTO deployments (id, project_id, number, status) VALUES (1, 1, 1, 'succeeded'), (2, 1, 2, 'succeeded')`)
	e.exec(t, `INSERT INTO deployment_artifacts (deployment_id, service, image_ref, image_digest)
		VALUES (1, 'web', 'shop:1', 'sha256:old'), (2, 'web', 'shop:2', 'sha256:new')`)
	if err := e.svc.SaveSettings(ctx, Settings{Auto: true, ImageDays: 3, KeepDeployments: 1, BuildCacheGB: 4}); err != nil {
		t.Fatal(err)
	}
	e.agent.PruneResult = agent.ImagePruneResult{Deleted: 2, ReclaimedBytes: 5e8, Failed: 1, Errors: []string{"abc: in use"}}
	e.agent.CacheResult = agent.BuildCachePruneResult{ReclaimedBytes: 209e8}

	r, err := e.svc.Run(ctx, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(e.agent.PruneReqs) != 1 {
		t.Fatalf("prune requests = %d", len(e.agent.PruneReqs))
	}
	req := e.agent.PruneReqs[0]
	if req.OlderThanSeconds != 3*24*60*60 {
		t.Errorf("older than = %d", req.OlderThanSeconds)
	}
	if len(req.ProtectedDigests) != 1 || req.ProtectedDigests[0] != "sha256:new" {
		t.Errorf("protected = %v, want only the last deployment's image", req.ProtectedDigests)
	}
	if len(e.agent.CacheKeeps) != 1 || e.agent.CacheKeeps[0] != 4e9 {
		t.Errorf("cache keeps = %v, want 4 GB", e.agent.CacheKeeps)
	}
	if r.Images.Failed != 1 || r.BuildCache.ReclaimedBytes != 209e8 || r.Error != "" || r.FinishedAt.IsZero() {
		t.Errorf("result = %+v", r)
	}
	last, err := e.svc.Last(ctx)
	if err != nil || last == nil || last.Images.Deleted != 2 || last.Trigger != "manual" || last.FinishedAt.IsZero() {
		t.Fatalf("last = %+v, %v", last, err)
	}
}

func TestRunRecordsAFailedStepAndRunsTheOther(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.agent.Fail["docker.prune"] = io.ErrUnexpectedEOF
	r, err := e.svc.Run(ctx, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Error == "" || len(e.agent.CacheKeeps) != 1 {
		t.Fatalf("result = %+v, cache keeps = %v, want the error and the cache still trimmed", r, e.agent.CacheKeeps)
	}
	if last, _ := e.svc.Last(ctx); last == nil || last.Error == "" {
		t.Fatalf("last = %+v, want the error kept", last)
	}
}

func TestRunRefusesWhileRunning(t *testing.T) {
	e := newEnv(t)
	e.svc.running.Lock()
	defer e.svc.running.Unlock()
	if _, err := e.svc.Run(context.Background(), "manual", ""); err != ErrBusy {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestAfterDeploy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.exec(t, `INSERT INTO projects (id, name) VALUES (1, 'shop'), (2, 'blog')`)

	// On by default.
	e.svc.AfterDeploy("shop")
	e.svc.Wait()
	if len(e.agent.PruneReqs) != 1 || len(e.audits) != 1 || e.audits[0]["project"] != "shop" {
		t.Fatalf("default run: prunes = %d, audits = %v", len(e.agent.PruneReqs), e.audits)
	}

	// Not while another project is still deploying, and the page is told.
	e.exec(t, `INSERT INTO deployments (project_id, number, status) VALUES (2, 1, 'pulling')`)
	e.svc.AfterDeploy("shop")
	e.svc.Wait()
	if len(e.agent.PruneReqs) != 1 {
		t.Fatalf("ran during another deployment: prunes = %d", len(e.agent.PruneReqs))
	}
	if last, _ := e.svc.Last(ctx); last == nil || last.Skipped != "a deployment of blog was still running" {
		t.Fatalf("last = %+v, want the skip recorded", last)
	}
	var deploying *DeployingError
	if _, err := e.svc.Run(ctx, "manual", ""); !errors.As(err, &deploying) || len(deploying.Projects) != 1 {
		t.Fatalf("manual run during a deployment: err = %v", err)
	}

	// A deployment stuck in a running status for over six hours, as after
	// a crash, does not hold the clean-up off for good.
	e.exec(t, `UPDATE deployments SET created_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-7 hours')`)
	e.svc.AfterDeploy("shop")
	e.svc.Wait()
	if len(e.agent.PruneReqs) != 2 {
		t.Fatalf("a stuck deployment held the clean-up off: prunes = %d", len(e.agent.PruneReqs))
	}

	// Not when switched off.
	off := Defaults
	off.Auto = false
	if err := e.svc.SaveSettings(ctx, off); err != nil {
		t.Fatal(err)
	}
	e.svc.AfterDeploy("blog")
	e.svc.Wait()
	if len(e.agent.PruneReqs) != 2 {
		t.Fatalf("ran when off: prunes = %d", len(e.agent.PruneReqs))
	}
}
