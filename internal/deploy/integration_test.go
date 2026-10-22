//go:build integration

// Full-stack deployment against real Docker + docker compose. Runs in CI:
//
//	go test -tags integration ./internal/deploy/
package deploy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent/local"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/git"
	"github.com/windlass-dev/windlass/internal/jobs"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/registries"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

const integrationCompose = `services:
  web:
    image: nginx:alpine
    restart: unless-stopped
`

func TestRealDeployEndToEnd(t *testing.T) {
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
	dep := New(q, ag, proj, gitSvc, registries.New(q, box, logger), runner, bus, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	const name = "windlass-it-deploy"
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-p", name, "down", "--volumes", "--remove-orphans").Run()
	})

	if _, err := proj.Create(ctx, projects.CreateReq{Name: name, Compose: integrationCompose}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := proj.SetEnv(ctx, name, map[string]string{"GREETING": "hello from windlass"}); err != nil {
		t.Fatalf("set env: %v", err)
	}

	d, err := dep.Deploy(ctx, name, "manual")
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	runnerCtx, stopRunner := context.WithCancel(ctx)
	go runner.Run(runnerCtx)

	// Wait for a terminal status.
	var final db.Deployment
	for {
		final, err = q.GetDeploymentByID(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.Status == "succeeded" || final.Status == "failed" || final.Status == "cancelled" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timeout; status=%s", final.Status)
		case <-time.After(500 * time.Millisecond):
		}
	}
	if final.Status != "succeeded" {
		evs, _ := dep.Events(ctx, d.ID, 0)
		var log strings.Builder
		for _, ev := range evs {
			log.WriteString(ev.Type + ": " + ev.Message + "\n")
		}
		t.Fatalf("deployment %s: %s\n%s", final.Status, final.Error.String, log.String())
	}

	// The container is genuinely running.
	out, err := exec.CommandContext(ctx, "docker", "ps", "--filter",
		"label=com.docker.compose.project="+name, "--format", "{{.Status}}").Output()
	if err != nil || !strings.Contains(string(out), "Up") {
		t.Fatalf("nginx not running: %q, %v", out, err)
	}

	// Artifacts carry a real digest.
	arts, err := q.ListDeploymentArtifacts(ctx, d.ID)
	if err != nil || len(arts) == 0 || !strings.Contains(arts[0].ImageDigest, "sha256:") {
		t.Fatalf("artifacts = %+v, %v", arts, err)
	}

	// Principle 7: stop the panel (runner), the app keeps running.
	stopRunner()
	time.Sleep(time.Second)
	out, err = exec.CommandContext(ctx, "docker", "ps", "--filter",
		"label=com.docker.compose.project="+name, "--format", "{{.Status}}").Output()
	if err != nil || !strings.Contains(string(out), "Up") {
		t.Fatalf("app died when panel stopped: %q, %v", out, err)
	}

	// The .env rendered by the pipeline is on disk, hand-editable.
	envPath := filepath.Join(dir, "projects", name, ".env")
	data, err := exec.Command("cat", envPath).Output()
	if err != nil || !strings.Contains(string(data), "GREETING") {
		t.Fatalf(".env = %q, %v", data, err)
	}
}

// A rollback of a Git project whose service is built on the server runs the
// earlier commit's code. Before the fix it pinned the built image by an ID
// the registry never had, and Compose failed with "build tag cannot contain
// a digest"; a build-only service kept running the newer code.
func TestRealRollbackRebuildsGitCommit(t *testing.T) {
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
	runner := jobs.NewRunner(q, logger)
	dep := New(q, ag, proj, git.New(q, box, logger), registries.New(q, box, logger), runner, bus, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	go runner.Run(ctx)

	// A repository whose image says which commit it was built from. The
	// bare repository Windlass clones fetches each commit from the work tree.
	work, bare := filepath.Join(dir, "work"), filepath.Join(dir, "site.git")
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitRun("init", "-q", "-b", "main", work)
	gitRun("init", "-q", "--bare", "-b", "main", bare)
	commit := func(version string) {
		t.Helper()
		dockerfile := "FROM busybox\nRUN echo " + version + " > /version\nCMD [\"sleep\", \"600\"]\n"
		if err := os.WriteFile(filepath.Join(work, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun("-C", work, "add", "Dockerfile")
		gitRun("-C", work, "commit", "-q", "-m", version)
		gitRun("-C", bare, "fetch", "-q", work, "main:main")
	}

	const name = "windlass-it-rollback"
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-p", name, "down", "--rmi", "local", "--remove-orphans").Run()
	})
	if _, err := proj.Create(ctx, projects.CreateReq{Name: name, Source: "git", GitRepo: bare, Branch: "main",
		Compose: "services:\n  web:\n    build: ./src\n    image: " + name + "-web:latest\n"}); err != nil {
		t.Fatal(err)
	}

	deploy := func(start func() (db.Deployment, error)) db.Deployment {
		t.Helper()
		d, err := start()
		if err != nil {
			t.Fatal(err)
		}
		for {
			final, err := q.GetDeploymentByID(ctx, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch final.Status {
			case "succeeded":
				return final
			case "failed", "cancelled":
				evs, _ := dep.Events(ctx, d.ID, 0)
				var log strings.Builder
				for _, ev := range evs {
					log.WriteString(ev.Type + ": " + ev.Message + "\n")
				}
				t.Fatalf("deployment #%d %s: %s\n%s", final.Number, final.Status, final.Error.String, log.String())
			}
			select {
			case <-ctx.Done():
				t.Fatalf("timeout; status=%s", final.Status)
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	running := func() string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", "compose", "-p", name, "exec", "-T", "web", "cat", "/version").Output()
		if err != nil {
			t.Fatalf("read version: %v", err)
		}
		return strings.TrimSpace(string(out))
	}

	commit("one")
	first := deploy(func() (db.Deployment, error) { return dep.Deploy(ctx, name, "manual") })
	commit("two")
	deploy(func() (db.Deployment, error) { return dep.Deploy(ctx, name, "manual") })
	if got := running(); got != "two" {
		t.Fatalf("second deployment runs %q", got)
	}

	deploy(func() (db.Deployment, error) { return dep.Rollback(ctx, name, first.Number) })
	if got := running(); got != "one" {
		t.Fatalf("after rolling back to #%d the container runs %q, want one", first.Number, got)
	}
}
