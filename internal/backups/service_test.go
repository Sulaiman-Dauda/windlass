package backups

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/agent/fake"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

func TestDatabaseContainerSelectsMatchingEngine(t *testing.T) {
	containers := []agent.Container{
		{ID: "web", ComposeService: "web", Image: "example/web:latest", State: "running"},
		{ID: "stopped-db", ComposeService: "postgres", Image: "postgres:17", State: "exited"},
		{ID: "pg", ComposeService: "database", Image: "postgres:17-alpine", State: "running"},
		{ID: "mysql", ComposeService: "mysql", Image: "mysql:8.4", State: "running"},
	}
	if got := databaseContainer(containers, "postgres"); got != "pg" {
		t.Fatalf("postgres target = %q, want pg", got)
	}
	if got := databaseContainer(containers, "mysql"); got != "mysql" {
		t.Fatalf("mysql target = %q, want mysql", got)
	}
	if got := databaseContainer(containers[:1], "postgres"); got != "" {
		t.Fatalf("unrelated container selected: %q", got)
	}
}

func TestBackupRunLockIsPerProject(t *testing.T) {
	s := &Service{running: map[string]bool{}}
	if !s.start("alpha") {
		t.Fatal("first alpha backup was rejected")
	}
	if s.start("alpha") {
		t.Fatal("overlapping alpha backup was accepted")
	}
	if !s.start("bravo") {
		t.Fatal("independent project backup was rejected")
	}
	s.done("alpha")
	if !s.start("alpha") {
		t.Fatal("alpha remained locked after completion")
	}
}

func TestPruneKeepsDestinationSeparateAndRecordOnDeleteFailure(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := store.Migrate(sqlDB, migrations.FS); err != nil {
		t.Fatal(err)
	}
	q := db.New(sqlDB)
	project, err := q.CreateProject(ctx, db.CreateProjectParams{Name: "shop", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	for i, destination := range []string{"local", "local", "local", "s3"} {
		record, err := q.CreateBackup(ctx, db.CreateBackupParams{
			ProjectID: project.ID, Kind: "manual", Destination: destination,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := q.FinishBackup(ctx, db.FinishBackupParams{
			ID: record.ID, Status: "done", Path: destination + "-" + string(rune('a'+i)),
			Error: sql.NullString{},
		}); err != nil {
			t.Fatal(err)
		}
	}

	ag := fake.New()
	svc := &Service{q: q, agent: ag, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ag.Fail["fs.rmarchive"] = errors.New("storage unavailable")
	svc.prune(ctx, project.ID, "local", 1)
	rows, err := q.ListProjectBackups(ctx, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("records after failed delete = %d, want 4", len(rows))
	}

	delete(ag.Fail, "fs.rmarchive")
	svc.prune(ctx, project.ID, "local", 1)
	rows, err = q.ListProjectBackups(ctx, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Destination != "s3" || rows[1].Destination != "local" {
		t.Fatalf("prune crossed destinations or kept wrong records: %+v", rows)
	}
}
