package backups

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/agent/fake"
	"github.com/windlass-dev/windlass/internal/dbdetect"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

func TestDatabaseContainerPrefersARunningExactMatch(t *testing.T) {
	containers := []agent.Container{
		{ID: "web", ComposeService: "web", Image: "example/web:latest", State: "running"},
		{ID: "exporter", ComposeService: "metrics", Image: "prometheuscommunity/postgres-exporter", State: "running"},
		{ID: "stopped-db", ComposeService: "postgres", Image: "postgres:17", State: "exited"},
		{ID: "pg", ComposeService: "database", Image: "docker.io/library/postgres:17-alpine", State: "running"},
	}
	if got, engine := databaseContainer(containers); got.ID != "pg" || engine != "postgres" {
		t.Fatalf("target = %q (%s), want pg (postgres)", got.ID, engine)
	}
	if got, _ := databaseContainer(containers[:3]); got.ID != "exporter" {
		t.Fatalf("with only a loose match running, target = %q, want exporter so the attempt is logged", got.ID)
	}
	if got, engine := databaseContainer([]agent.Container{containers[0], containers[2]}); got.ID != "stopped-db" || engine != "postgres" {
		t.Fatalf("with no running database, target = %q (%s), want stopped-db so the skip is logged", got.ID, engine)
	}
	if _, engine := databaseContainer(containers[:1]); engine != "" {
		t.Fatalf("a non-database container was taken for a %s database", engine)
	}
}

func TestDatabaseEngine(t *testing.T) {
	for _, tt := range []struct {
		service, image, want string
		exact                bool
	}{
		{"db", "postgres:18-alpine", "postgres", true},
		{"db", "postgis/postgis:16-3.4", "postgres", true},
		{"db", "pgvector/pgvector:pg17", "postgres", true},
		{"db", "postgres@sha256:abc", "postgres", true},
		{"db", "localhost:5000/postgres:17", "postgres", true},
		{"postgres", "registry.example.com/team/custom-pg:1", "postgres", true},
		{"db", "mysql:8.4", "mysql", true},
		{"db", "mariadb:11", "mysql", true},
		{"MariaDB", "example/db:1", "mysql", true},
		{"db", "container-registry.oracle.com/mysql/community-server:8.4", "mysql", false},
		{"db", "bitnami/postgresql-repmgr:16", "postgres", false},
		{"db", "yobasystems/alpine-mariadb", "mysql", false},
		{"db", "ghcr.io/acme/app-postgres:2", "postgres", false},
		{"metrics", "prometheuscommunity/postgres-exporter", "postgres", false},
		{"web", "ghost:5-alpine", "", false},
		{"cache", "redis:7", "", false},
	} {
		got, exact := dbdetect.Engine(tt.service, tt.image)
		if got != tt.want || exact != tt.exact {
			t.Errorf("Engine(%s, %s) = %q exact=%v, want %q exact=%v", tt.service, tt.image, got, exact, tt.want, tt.exact)
		}
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
	t.Cleanup(func() { _ = sqlDB.Close() })
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

// newRunEnv wires a backup service to the fake agent with one project, "shop".
func newRunEnv(t *testing.T) (*Service, *fake.Fake, *db.Queries) {
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, _ := secrets.New(bytes.Repeat([]byte{1}, 32))
	ag := fake.New()
	bus := events.NewBus()
	proj := projects.New(q, ag, box, bus, logger)
	if _, err := proj.Create(context.Background(), projects.CreateReq{Name: "shop"}); err != nil {
		t.Fatal(err)
	}
	return New(q, ag, proj, box, bus, logger), ag, q
}

func TestBackupRecordsWhatHappenedToTheDatabaseDump(t *testing.T) {
	web := agent.Container{ID: "web", Name: "shop-web-1", ComposeProject: "shop", ComposeService: "web", Image: "nginx", State: "running"}
	pg := agent.Container{ID: "pg", Name: "shop-db-1", ComposeProject: "shop", ComposeService: "db", Image: "postgres:17", State: "running"}
	stopped := pg
	stopped.State = "exited"

	for _, tt := range []struct {
		name       string
		containers []agent.Container
		script     *fake.ExecScript
		want       string
		reason     string
		inArchive  bool
	}{
		{"no database", []agent.Container{web}, nil, dumpNone, "", false},
		{"database service has no container", []agent.Container{web}, nil, dumpFailed, "database service db has no container", false},
		{"database stopped", []agent.Container{web, stopped}, nil, dumpFailed, "database container shop-db-1 is not running", false},
		{"dump fails", []agent.Container{web, pg}, &fake.ExecScript{Output: "partial", Exit: 1}, dumpFailed, "the dump failed: exit 1", false},
		{"dump empty", []agent.Container{web, pg}, &fake.ExecScript{}, dumpFailed, "the dump produced no output", false},
		{"dump taken", []agent.Container{web, pg}, &fake.ExecScript{Output: "CREATE TABLE marker;"}, dumpIncluded, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, ag, q := newRunEnv(t)
			ag.Containers = tt.containers
			if tt.name == "database service has no container" {
				ag.Resolved["shop"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
					"web": {Image: "nginx"}, "db": {Image: "postgres:17"},
				}}
			}
			if tt.script != nil {
				ag.ExecScripts["pg"] = *tt.script
			}
			rec, err := s.Run(context.Background(), "shop", "manual", "local")
			if err != nil {
				t.Fatalf("backup failed: %v", err)
			}
			rows, err := q.ListProjectBackups(context.Background(), "shop")
			if err != nil || len(rows) != 1 {
				t.Fatalf("rows = %d, %v", len(rows), err)
			}
			for _, got := range []db.Backup{rec, rows[0]} {
				if got.Status != "done" || got.DbDump.String != tt.want || got.DbDumpError.String != tt.reason {
					t.Fatalf("record = status %q, db_dump %q, reason %q; want done, %q, %q",
						got.Status, got.DbDump.String, got.DbDumpError.String, tt.want, tt.reason)
				}
			}
			dump, ok := ag.Archives[rec.Path][dumpArchivePath]
			if ok != tt.inArchive || (ok && string(dump) != tt.script.Output) {
				t.Fatalf("archive dump = %q (present %v), want present %v", dump, ok, tt.inArchive)
			}
			if len(ag.Spools) != 0 {
				t.Fatalf("spooled dump left behind: %v", ag.Spools)
			}
		})
	}
}

// A backup taken before migration 0012 has no db_dump, which the API must
// show as unknown rather than as "no database".
func TestOlderBackupHasNoDumpRecord(t *testing.T) {
	s, _, q := newRunEnv(t)
	p, err := s.projects.Get(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := q.CreateBackup(context.Background(), db.CreateBackupParams{ProjectID: p.ID, Kind: "manual", Destination: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.DbDump.Valid || rec.DbDumpError.Valid {
		t.Fatalf("new row has a dump record before it finished: %+v", rec)
	}
}
