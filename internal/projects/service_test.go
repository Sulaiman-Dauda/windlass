package projects

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent/fake"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

func testService(t *testing.T, ag *fake.Fake) (*Service, *db.Queries) {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "windlass.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := store.Migrate(database, migrations.FS); err != nil {
		t.Fatal(err)
	}
	queries := db.New(database)
	box, err := secrets.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(queries, ag, box, events.NewBus(), logger), queries
}

func TestReconcileRebuildsIndexFromFilesystem(t *testing.T) {
	ctx := context.Background()
	ag := fake.New()
	ag.Files["imported"] = map[string][]byte{
		"compose.yaml": []byte("services:\n  web:\n    image: nginx:alpine\n"),
		".env":         []byte("# hand edited\nGREETING=hello=world\n"),
		manifestFile: []byte(`{
  "version": 1,
  "source": "git",
  "git_repo": "https://example.com/acme/app.git",
  "git_branch": "main",
  "auto_deploy": true,
  "domains": [{"hostname":"app.example.com","service":"web","container_port":80}]
}`),
	}

	service, queries := testService(t, ag)
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	project, err := queries.GetProjectByName(ctx, "imported")
	if err != nil {
		t.Fatal(err)
	}
	if project.Source != "git" || project.AutoDeploy != 1 {
		t.Fatalf("project index = %+v", project)
	}
	domains, err := queries.ListProjectDomains(ctx, project.ID)
	if err != nil || len(domains) != 1 || domains[0].Hostname != "app.example.com" {
		t.Fatalf("domains = %+v, %v", domains, err)
	}
	vars, err := service.GetEnv(ctx, "imported")
	if err != nil || vars["GREETING"] != "hello=world" {
		t.Fatalf("env = %#v, %v", vars, err)
	}

	// A later SSH edit is authoritative and is imported on the next read.
	ag.Files["imported"][".env"] = []byte("GREETING=changed-over-ssh\n")
	vars, err = service.GetEnv(ctx, "imported")
	if err != nil || vars["GREETING"] != "changed-over-ssh" {
		t.Fatalf("edited env = %#v, %v", vars, err)
	}
}

func TestParseEnvFile(t *testing.T) {
	vars, err := parseEnvFile("# comment\nexport A=one\nB=\"two words\"\nTOKEN=a=b=c\n")
	if err != nil {
		t.Fatal(err)
	}
	if vars["A"] != "one" || vars["B"] != "two words" || vars["TOKEN"] != "a=b=c" {
		t.Fatalf("vars = %#v", vars)
	}
}

func TestMissingDirectoryDoesNotDeletePlatformHistory(t *testing.T) {
	ctx := context.Background()
	ag := fake.New()
	service, queries := testService(t, ag)
	project, err := service.Create(ctx, CreateReq{Name: "durable"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ProjectID: project.ID, ProjectID_2: project.ID, TriggeredBy: "manual",
	}); err != nil {
		t.Fatal(err)
	}

	delete(ag.Files, "durable") // simulates a missing/unmounted stacks disk
	projects, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("missing project should be hidden: %+v", projects)
	}
	if _, err := queries.GetProjectByName(ctx, "durable"); err != nil {
		t.Fatalf("platform project index was destroyed: %v", err)
	}
	deployments, err := queries.ListDeployments(ctx, db.ListDeploymentsParams{
		Name: "durable", Limit: 10,
	})
	if err != nil || len(deployments) != 1 {
		t.Fatalf("deployment history was destroyed: %+v, %v", deployments, err)
	}
}
