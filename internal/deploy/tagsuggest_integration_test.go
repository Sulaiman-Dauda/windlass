//go:build integration

package deploy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
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

// A real deploy of a tag Docker Hub does not have fails in the pull, and
// the suggestion reads the real daemon wording and real Docker Hub tags.
func TestRealMissingTagSuggestion(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	const name = "windlass-it-badtag"
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-p", name, "down", "--volumes", "--remove-orphans").Run()
	})
	compose := "services:\n  web:\n    image: nginx:1.27.999\n"
	if _, err := proj.Create(ctx, projects.CreateReq{Name: name, Compose: compose}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	d, err := dep.Deploy(ctx, name, "manual")
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	go runner.Run(ctx)

	var final db.Deployment
	for {
		if final, err = q.GetDeploymentByID(ctx, d.ID); err != nil {
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
	if final.Status != "failed" {
		t.Fatalf("status = %s, want failed", final.Status)
	}
	t.Logf("error_line = %q\nerror = %q", final.ErrorLine.String, final.Error.String)

	if evs, _ := dep.Events(ctx, d.ID, 0); true {
		for _, ev := range evs {
			t.Logf("%s: %q", ev.Type, ev.Message)
		}
	}
	sugs, err := dep.TagSuggestions(ctx, final)
	if err != nil {
		t.Fatalf("suggestion: %v (error_line %q, error %q)", err, final.ErrorLine.String, final.Error.String)
	}
	if len(sugs) != 1 {
		t.Fatalf("suggestions = %+v", sugs)
	}
	sug := sugs[0]
	if sug.Repository != "library/nginx" || sug.Tag != "1.27.999" || len(sug.Tags) == 0 || !strings.HasPrefix(sug.Tags[0], "1.27.") {
		t.Fatalf("suggestion = %+v", sug)
	}
	t.Logf("suggested %v", sug.Tags)

	// Turned off, nothing is looked up.
	if err := dep.SetTagSuggestEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := dep.TagSuggestions(ctx, final); err != ErrNoTagSuggestion {
		t.Fatalf("off: err = %v", err)
	}
}
