package attention

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/agent/fake"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/internal/update"
	"github.com/windlass-dev/windlass/migrations"
)

type env struct {
	q     *db.Queries
	agent *fake.Fake
	panel *panelStub
	up    *updateStub
	svc   *Service
}

type panelStub struct{ host string }

func (p *panelStub) PanelDomain(context.Context) (string, error) { return p.host, nil }

type updateStub struct {
	rel   update.Release
	err   error
	calls atomic.Int32
}

func (u *updateStub) Check(context.Context) (update.Release, error) {
	u.calls.Add(1)
	return u.rel, u.err
}

func newEnv(t *testing.T) *env {
	t.Helper()
	sqlDB, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := store.Migrate(sqlDB, migrations.FS); err != nil {
		t.Fatal(err)
	}
	q := db.New(sqlDB)
	ag := fake.New()
	panel := &panelStub{}
	up := &updateStub{}
	svc := New(q, ag, panel, up, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.TTL = 0
	return &env{q: q, agent: ag, panel: panel, up: up, svc: svc}
}

func (e *env) project(t *testing.T, name string) db.Project {
	t.Helper()
	p, err := e.q.CreateProject(context.Background(), db.CreateProjectParams{Name: name, Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) deploy(t *testing.T, p db.Project, status, step, errText, line string) {
	t.Helper()
	ctx := context.Background()
	d, err := e.q.CreateDeployment(ctx, db.CreateDeploymentParams{ProjectID: p.ID, ProjectID_2: p.ID, TriggeredBy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.q.FinishDeployment(ctx, db.FinishDeploymentParams{Status: status,
		Error: sql.NullString{String: errText, Valid: errText != ""}, ID: d.ID}); err != nil {
		t.Fatal(err)
	}
	if step != "" {
		if err := e.q.RecordDeploymentFailure(ctx, db.RecordDeploymentFailureParams{
			FailedStep: sql.NullString{String: step, Valid: true},
			ErrorLine:  sql.NullString{String: line, Valid: line != ""}, ID: d.ID}); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *env) list(t *testing.T) []Item {
	t.Helper()
	l, err := e.svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l.CheckedAt.IsZero() {
		t.Error("checked_at unset")
	}
	return l.Items
}

func kinds(items []Item) string {
	var k []string
	for _, i := range items {
		k = append(k, i.Kind+":"+i.Project)
	}
	return strings.Join(k, " ")
}

func TestEmptyServerHasNothing(t *testing.T) {
	e := newEnv(t)
	if items := e.list(t); len(items) != 0 {
		t.Fatalf("items = %+v", items)
	}
}

func TestLatestFailedDeployOnly(t *testing.T) {
	e := newEnv(t)
	shop := e.project(t, "shop")
	e.deploy(t, shop, "succeeded", "", "", "")
	e.deploy(t, shop, "failed", "pulling", "docker compose pull: exit status 1\nmore", "manifest for acme/web:1.9 not found")
	// A failure followed by a success is history, not a row.
	blog := e.project(t, "blog")
	e.deploy(t, blog, "failed", "building", "build failed", "")
	e.deploy(t, blog, "succeeded", "", "", "")
	// Never succeeded.
	api := e.project(t, "api")
	e.deploy(t, api, "failed", "verifying", "service web is unhealthy", "")

	items := e.list(t)
	if got := kinds(items); got != "deploy_failed:api deploy_failed:shop" {
		t.Fatalf("items = %s", got)
	}
	shopItem := items[1]
	if shopItem.Title != "shop: deploy #2 failed while pulling images" {
		t.Errorf("title = %q", shopItem.Title)
	}
	if shopItem.Detail != "manifest for acme/web:1.9 not found. The last successful deploy is #1." {
		t.Errorf("detail = %q", shopItem.Detail)
	}
	if shopItem.Tone != ToneError || shopItem.Link != "/projects/shop/deployments" {
		t.Errorf("item = %+v", shopItem)
	}
	if items[0].Title != "api: deploy #1 failed its health checks" ||
		items[0].Detail != "service web is unhealthy. No deploy of this project has succeeded yet." {
		t.Errorf("api = %+v", items[0])
	}
}

func TestDiskThresholdAndSetting(t *testing.T) {
	e := newEnv(t)
	e.agent.Metrics = agent.HostMetrics{DiskUsed: 84e9, DiskTotal: 100e9}
	if items := e.list(t); len(items) != 0 {
		t.Fatalf("84%% is under the default 85%%: %+v", items)
	}
	e.agent.Metrics.DiskUsed = 85e9
	items := e.list(t)
	if len(items) != 1 || items[0].Kind != KindDisk || items[0].Title != "Disk is 85% full" ||
		items[0].Detail != "15.0 GB free of 100.0 GB on the projects volume." {
		t.Fatalf("items = %+v", items)
	}

	ctx := context.Background()
	if err := e.q.SetSetting(ctx, db.SetSettingParams{Key: DiskPercentSetting, Value: "90"}); err != nil {
		t.Fatal(err)
	}
	if items := e.list(t); len(items) != 0 {
		t.Fatalf("threshold 90: %+v", items)
	}
	// Nonsense falls back to the default rather than switching the row off.
	if err := e.q.SetSetting(ctx, db.SetSettingParams{Key: DiskPercentSetting, Value: "100"}); err != nil {
		t.Fatal(err)
	}
	if items := e.list(t); len(items) != 1 {
		t.Fatalf("threshold 100 must fall back to 85: %+v", items)
	}
}

func TestVolumesWithoutSchedule(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	db1 := e.project(t, "database")
	backed := e.project(t, "backed")
	e.project(t, "stateless")
	staging := e.project(t, "database-staging")
	if err := e.q.SetProjectStagingOf(ctx, db.SetProjectStagingOfParams{
		StagingOf: sql.NullString{String: "database", Valid: true}, ID: staging.ID}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertBackupSchedule(ctx, db.UpsertBackupScheduleParams{ProjectID: backed.ID,
		Interval: "daily", Destination: "local", RetentionCount: 7, Enabled: 1}); err != nil {
		t.Fatal(err)
	}
	// A disabled schedule is no schedule.
	if err := e.q.UpsertBackupSchedule(ctx, db.UpsertBackupScheduleParams{ProjectID: db1.ID,
		Interval: "daily", Destination: "local", RetentionCount: 7, Enabled: 0}); err != nil {
		t.Fatal(err)
	}
	e.agent.Containers = []agent.Container{
		{ComposeProject: "database", ComposeService: "db", Volumes: []string{"database_pgdata"}},
		{ComposeProject: "database", ComposeService: "cache", Volumes: []string{"database_redis", "database_pgdata"}},
		{ComposeProject: "backed", Volumes: []string{"backed_data"}},
		{ComposeProject: "stateless"},
		{ComposeProject: "database-staging", Volumes: []string{"database-staging_pgdata"}},
	}
	items := e.list(t)
	if got := kinds(items); got != "no_backup:database" {
		t.Fatalf("items = %s", got)
	}
	if items[0].Title != "database has volumes and no backup schedule" ||
		!strings.HasPrefix(items[0].Detail, "Volumes pgdata, redis. ") ||
		items[0].Link != "/projects/database/backups" {
		t.Errorf("item = %+v", items[0])
	}

	// Docker down: the rows that need it go, the rest stay.
	e.agent.Fail["docker.list"] = errors.New("docker down")
	e.agent.Metrics = agent.HostMetrics{DiskUsed: 99, DiskTotal: 100}
	if got := kinds(e.list(t)); got != "disk:" {
		t.Fatalf("with docker down = %s", got)
	}
}

func TestProxyOnlyWhenSomethingUsesIt(t *testing.T) {
	e := newEnv(t)
	e.agent.ProxyAvailable = false
	if items := e.list(t); len(items) != 0 {
		t.Fatalf("no domains, Caddy down is not a row: %+v", items)
	}
	e.panel.host = "panel.example.com"
	if got := kinds(e.list(t)); got != "proxy:" {
		t.Fatalf("panel domain = %s", got)
	}
	e.panel.host = ""
	p := e.project(t, "site")
	if _, err := e.q.CreateDomain(context.Background(), db.CreateDomainParams{ProjectID: p.ID,
		Hostname: "site.example.com", Service: "web", ContainerPort: 80}); err != nil {
		t.Fatal(err)
	}
	if got := kinds(e.list(t)); got != "proxy:" {
		t.Fatalf("project domain = %s", got)
	}
	e.agent.ProxyAvailable = true
	if items := e.list(t); len(items) != 0 {
		t.Fatalf("Caddy up: %+v", items)
	}
}

func TestUpdateCheckedInBackgroundAndCached(t *testing.T) {
	e := newEnv(t)
	e.up.rel = update.Release{Version: "v0.63", CurrentVer: "v0.62", UpdateReady: true, ApplySupported: true}

	// The first list never waits for GitHub.
	if items := e.list(t); len(items) != 0 {
		t.Fatalf("first list = %+v", items)
	}
	waitFor(t, func() bool {
		e.svc.upMu.Lock()
		defer e.svc.upMu.Unlock()
		return !e.svc.upRunning && e.up.calls.Load() == 1
	})
	items := e.list(t)
	if len(items) != 1 || items[0].Kind != KindUpdate || items[0].Title != "Windlass v0.63 is available" {
		t.Fatalf("items = %+v", items)
	}
	e.list(t)
	if n := e.up.calls.Load(); n != 1 {
		t.Errorf("update checked %d times, want once while cached", n)
	}
}

func TestUpdateFailureWaitsToRetry(t *testing.T) {
	e := newEnv(t)
	e.up.err = errors.New("rate limited")
	e.list(t)
	waitFor(t, func() bool {
		e.svc.upMu.Lock()
		defer e.svc.upMu.Unlock()
		return !e.svc.upRunning && e.up.calls.Load() == 1
	})
	e.list(t)
	e.list(t)
	time.Sleep(20 * time.Millisecond)
	if n := e.up.calls.Load(); n != 1 {
		t.Errorf("failed check retried %d times inside UpdateRetry", n)
	}
}

func TestListIsCached(t *testing.T) {
	e := newEnv(t)
	e.svc.TTL = time.Hour
	e.list(t)
	before := len(e.agent.Calls)
	e.list(t)
	if after := len(e.agent.Calls); after != before {
		t.Errorf("a cached list called the agent: %v", e.agent.Calls[before:])
	}
	e.svc.Invalidate()
	e.list(t)
	if len(e.agent.Calls) == before {
		t.Error("Invalidate did not force a new check")
	}
}

func TestOrderErrorsFirst(t *testing.T) {
	e := newEnv(t)
	e.agent.Metrics = agent.HostMetrics{DiskUsed: 95, DiskTotal: 100}
	p := e.project(t, "zed")
	e.deploy(t, p, "failed", "applying", "up failed", "")
	if got := kinds(e.list(t)); got != "deploy_failed:zed disk:" {
		t.Fatalf("order = %s", got)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The list is shared, so a request that ends mid-check must not leave a
// false Caddy row cached for everyone.
func TestCancelledRequestDoesNotCacheAFalseRow(t *testing.T) {
	e := newEnv(t)
	e.svc.TTL = time.Hour
	e.panel.host = "panel.example.com"
	e.agent.ProxyAvailable = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l, err := e.svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(l.Items); got != "" {
		t.Fatalf("items = %s", got)
	}
}
