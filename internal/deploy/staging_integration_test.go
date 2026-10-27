//go:build integration

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

// TestRealStagingPromotion runs a Git project and its staging on real
// Docker: staging deploys a new commit while live keeps the old one, the
// promotion puts staging's commit on live, a commit only on a branch is
// refused before live's checkout moves, and a fresh database finds the
// link again with every container still running.
func TestRealStagingPromotion(t *testing.T) {
	dir := t.TempDir()
	open := func(path string) (*db.Queries, func()) {
		t.Helper()
		sqlDB, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Migrate(sqlDB, migrations.FS); err != nil {
			t.Fatal(err)
		}
		return db.New(sqlDB), func() { sqlDB.Close() }
	}
	q, closeDB := open(filepath.Join(dir, "test.db"))
	defer closeDB()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, _ := secrets.New(bytes.Repeat([]byte{1}, 32))
	bus := events.NewBus()
	projectsDir := filepath.Join(dir, "projects")
	ag, err := local.New(local.Config{ProjectsDir: projectsDir})
	if err != nil {
		t.Fatal(err)
	}
	proj := projects.New(q, ag, box, bus, logger)
	runner := jobs.NewRunner(q, logger)
	dep := New(q, ag, proj, git.New(q, box, logger), registries.New(q, box, logger), runner, bus, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	go runner.Run(ctx)

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
	commit := func(branch, version string) {
		t.Helper()
		dockerfile := "FROM busybox\nRUN echo " + version + " > /version\nCMD [\"sleep\", \"600\"]\n"
		if err := os.WriteFile(filepath.Join(work, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun("-C", work, "add", "Dockerfile")
		gitRun("-C", work, "commit", "-q", "-m", version)
		gitRun("-C", bare, "fetch", "-q", work, branch+":"+branch)
	}

	const live, staging = "windlass-it-promote", "windlass-it-promote-staging"
	t.Cleanup(func() {
		for _, p := range []string{live, staging} {
			exec.Command("docker", "compose", "-p", p, "down", "--volumes", "--rmi", "local", "--remove-orphans").Run()
		}
	})
	if _, err := proj.Create(ctx, projects.CreateReq{Name: live, Source: "git", GitRepo: bare, Branch: "main",
		Compose: "services:\n  web:\n    build: ./src\n"}); err != nil {
		t.Fatal(err)
	}

	deploy := func(start func() (db.Deployment, error), want string) db.Deployment {
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
			case "succeeded", "failed", "cancelled":
				if final.Status != want {
					evs, _ := dep.Events(ctx, d.ID, 0)
					var log strings.Builder
					for _, ev := range evs {
						log.WriteString(ev.Type + ": " + ev.Message + "\n")
					}
					t.Fatalf("deployment #%d %s, want %s: %s\n%s", final.Number, final.Status, want, final.Error.String, log.String())
				}
				return final
			}
			select {
			case <-ctx.Done():
				t.Fatalf("timeout; status=%s", final.Status)
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	running := func(project string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", "compose", "-p", project, "exec", "-T", "web", "cat", "/version").Output()
		if err != nil {
			t.Fatalf("read %s version: %v", project, err)
		}
		return strings.TrimSpace(string(out))
	}

	commit("main", "one")
	deploy(func() (db.Deployment, error) { return dep.Deploy(ctx, live, "manual") }, "succeeded")

	if _, err := proj.CreateStagingCopy(ctx, projects.CopyReq{Live: live}); err != nil {
		t.Fatal(err)
	}
	deploy(func() (db.Deployment, error) { return dep.DeployFirst(ctx, staging, "") }, "succeeded")
	if got := running(staging); got != "one" {
		t.Fatalf("staging runs %q, want one", got)
	}

	commit("main", "two")
	tested := deploy(func() (db.Deployment, error) { return dep.Deploy(ctx, staging, "webhook") }, "succeeded")
	if got, was := running(staging), running(live); got != "two" || was != "one" {
		t.Fatalf("after staging's deploy: staging %q, live %q", got, was)
	}

	// Main moves on before the promotion; live gets what staging tested.
	commit("main", "three")
	promoted := deploy(func() (db.Deployment, error) { return dep.Promote(ctx, staging, tested.Number, "manual") }, "succeeded")
	if got := running(live); got != "two" {
		t.Fatalf("after promoting staging #%d live runs %q, want two", tested.Number, got)
	}
	if promoted.GitCommit.String != tested.GitCommit.String || promoted.PromotedFrom.Int64 != tested.ID {
		t.Errorf("promotion = commit %s from %d", promoted.GitCommit.String, promoted.PromotedFrom.Int64)
	}

	// Staging tracks a feature branch whose commit is not on main.
	gitRun("-C", work, "checkout", "-q", "-b", "feature")
	commit("feature", "four")
	stg, err := proj.Get(ctx, staging)
	if err != nil {
		t.Fatal(err)
	}
	if err := proj.SetGitMetadata(ctx, staging, stg.GitRepo.String, "feature", false); err != nil {
		t.Fatal(err)
	}
	if err := proj.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	branchOnly := deploy(func() (db.Deployment, error) { return dep.Deploy(ctx, staging, "manual") }, "succeeded")
	if got := running(staging); got != "four" {
		t.Fatalf("staging on feature runs %q", got)
	}
	before, err := os.ReadFile(filepath.Join(projectsDir, live, "src", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	refused := deploy(func() (db.Deployment, error) { return dep.Promote(ctx, staging, branchOnly.Number, "manual") }, "failed")
	if !strings.Contains(refused.Error.String, "is not on main yet. Merge it, then promote") {
		t.Errorf("refusal = %s", refused.Error.String)
	}
	after, _ := os.ReadFile(filepath.Join(projectsDir, live, "src", "Dockerfile"))
	if !bytes.Equal(before, after) {
		t.Errorf("live's checkout moved:\n%s", after)
	}
	if got := running(live); got != "two" {
		t.Errorf("after the refusal live runs %q", got)
	}

	// A fresh database over the same files rebuilds the link, and nothing
	// stopped meanwhile.
	q2, closeDB2 := open(filepath.Join(dir, "fresh.db"))
	defer closeDB2()
	fresh := projects.New(q2, ag, box, bus, logger)
	if err := fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := q2.GetProjectByName(ctx, staging); err != nil || p.StagingOf.String != live {
		t.Errorf("fresh index: staging_of = %q, %v", p.StagingOf.String, err)
	}
	if got, was := running(staging), running(live); got != "four" || was != "two" {
		t.Errorf("after the rebuild: staging %q, live %q", got, was)
	}

	// Deleting staging takes its volumes and frees live to deploy itself.
	if err := proj.Delete(ctx, staging); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := proj.StagingFor(ctx, live); ok || err != nil {
		t.Errorf("live still has a staging: %v", err)
	}
	out, _ := exec.Command("docker", "compose", "-p", staging, "ps", "-a", "-q").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("staging containers left: %s", out)
	}
}
