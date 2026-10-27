//go:build integration

package staging

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent/local"
	"github.com/windlass-dev/windlass/internal/deploy"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/git"
	"github.com/windlass-dev/windlass/internal/jobs"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/proxy"
	"github.com/windlass-dev/windlass/internal/registries"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

// The shape of an app that keeps files in a volume beside a Postgres
// database, as most of the projects Windlass runs do.
const dataCompose = `services:
  app:
    image: busybox
    command: ["sleep", "600"]
    volumes:
      - files:/data
  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_PASSWORD: integration
    volumes:
      - pgdata:/var/lib/postgresql/data
volumes:
  files: {}
  pgdata: {}
`

func TestRealStagingCopiesData(t *testing.T) {
	dir := t.TempDir()
	sqlDB, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := store.Migrate(sqlDB, migrations.FS); err != nil {
		t.Fatal(err)
	}
	q := db.New(sqlDB)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, _ := secrets.New(bytes.Repeat([]byte{1}, 32))
	bus := events.NewBus()
	ag, err := local.New(local.Config{ProjectsDir: filepath.Join(dir, "projects")})
	if err != nil {
		t.Fatal(err)
	}
	proj := projects.New(q, ag, box, bus, logger)
	gitSvc := git.New(q, box, logger)
	runner := jobs.NewRunner(q, logger)
	dep := deploy.New(q, ag, proj, gitSvc, registries.New(q, box, logger), runner, bus, logger)
	svc := New(q, ag, proj, dep, gitSvc, proxy.New(q, ag, proj, bus, logger), logger)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	go runner.Run(ctx)

	const live, staging = "windlass-it-seed", "windlass-it-seed-staging"
	t.Cleanup(func() {
		for _, p := range []string{staging, live} {
			exec.Command("docker", "compose", "-p", p, "down", "--volumes", "--remove-orphans").Run()
		}
	})
	if _, err := proj.Create(ctx, projects.CreateReq{Name: live, Compose: dataCompose}); err != nil {
		t.Fatal(err)
	}

	wait := func(project string, number int64) {
		t.Helper()
		for {
			d, err := dep.Get(ctx, project, number)
			if err != nil {
				t.Fatal(err)
			}
			switch d.Status {
			case "succeeded":
				return
			case "failed", "cancelled":
				evs, _ := dep.Events(ctx, d.ID, 0)
				var log strings.Builder
				for _, ev := range evs {
					log.WriteString(ev.Type + ": " + ev.Message + "\n")
				}
				t.Fatalf("%s #%d %s: %s\n%s", project, number, d.Status, d.Error.String, log.String())
			}
			select {
			case <-ctx.Done():
				t.Fatalf("timeout; %s #%d is %s", project, number, d.Status)
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	sh := func(project, service, script string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", "compose", "-p", project, "exec", "-T", service, "sh", "-c", script).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", project, service, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	psql := func(project, sql string) string {
		t.Helper()
		for i := 0; ; i++ {
			out, err := exec.CommandContext(ctx, "docker", "compose", "-p", project, "exec", "-T", "db",
				"psql", "-h", "127.0.0.1", "-U", "postgres", "-tAc", sql).CombinedOutput()
			if err == nil {
				return strings.TrimSpace(string(out))
			}
			if i > 60 {
				t.Fatalf("%s psql %q: %v\n%s", project, sql, err, out)
			}
			time.Sleep(time.Second)
		}
	}
	checksum := func(volume string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "-v", volume+":/v:ro", "busybox",
			"sh", "-c", "cd /v && find . -type f | sort | xargs md5sum").CombinedOutput()
		if err != nil {
			t.Fatalf("checksum %s: %v\n%s", volume, err, out)
		}
		return string(out)
	}

	d, err := dep.Deploy(ctx, live, "manual")
	if err != nil {
		t.Fatal(err)
	}
	wait(live, d.Number)
	sh(live, "app", "echo stock > /data/app.db && mkdir -p /data/uploads && echo photo > /data/uploads/a.jpg")
	psql(live, "CREATE TABLE orders (id int); INSERT INTO orders SELECT generate_series(1, 250)")
	before := checksum(live + "_files")

	pf, err := svc.Preflight(ctx, live, Request{CopyData: true})
	if err != nil {
		t.Fatal(err)
	}
	if !pf.CanCreate || len(pf.Data) == 0 {
		t.Fatalf("preflight = %+v", pf)
	}
	res, err := svc.Create(ctx, live, Request{CopyData: true}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wait(staging, res.Deployment)

	if got := sh(staging, "app", "cat /data/app.db /data/uploads/a.jpg"); got != "stock\nphoto" {
		t.Errorf("staging files = %q", got)
	}
	if got := psql(staging, "SELECT count(*) FROM orders"); got != "250" {
		t.Errorf("staging has %s orders, want 250", got)
	}
	if after := checksum(live + "_files"); after != before {
		t.Errorf("live's volume changed:\n%s\nwas\n%s", after, before)
	}

	// Staging writes stay in staging.
	sh(staging, "app", "echo test > /data/staging-only")
	psql(staging, "INSERT INTO orders VALUES (999)")
	if got := psql(live, "SELECT count(*) FROM orders"); got != "250" {
		t.Errorf("live has %s orders after staging wrote", got)
	}
	if checksum(live+"_files") != before {
		t.Error("staging's write reached live's volume")
	}

	// Model the persisted failure after an interrupted first copy. The
	// staging-only file and extra row stand for partially loaded old data.
	first, err := dep.Get(ctx, staging, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.FinishDeployment(ctx, db.FinishDeploymentParams{ID: first.ID, Status: "failed",
		Error: sql.NullString{String: deploy.InterruptedDataCopyMessage, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	res, err = svc.Recreate(ctx, staging, true, "")
	if err != nil {
		t.Fatalf("recreate interrupted staging: %v", err)
	}
	wait(staging, res.Deployment)
	if got := sh(staging, "app", "test ! -e /data/staging-only && cat /data/app.db /data/uploads/a.jpg"); got != "stock\nphoto" {
		t.Errorf("replacement files = %q", got)
	}
	if got := psql(staging, "SELECT count(*) FROM orders"); got != "250" {
		t.Errorf("replacement has %s orders, want a fresh copy of live's 250", got)
	}
	if checksum(live+"_files") != before || psql(live, "SELECT count(*) FROM orders") != "250" {
		t.Error("recreating staging changed live's data")
	}

	// Delete and create again without data: the volumes start clean.
	if err := proj.Delete(ctx, staging); err != nil {
		t.Fatal(err)
	}
	res, err = svc.Create(ctx, live, Request{}, "")
	if err != nil {
		t.Fatalf("create again: %v", err)
	}
	wait(staging, res.Deployment)
	if got := sh(staging, "app", "ls -A /data"); got != "" {
		t.Errorf("recreated staging's volume holds %q", got)
	}
	if got := psql(staging, "SELECT to_regclass('public.orders') IS NULL"); got != "t" {
		t.Errorf("recreated staging still has the orders table")
	}
}
