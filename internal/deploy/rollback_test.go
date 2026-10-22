package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/store/db"
)

func TestPinnedRef(t *testing.T) {
	cases := []struct{ ref, digest, want string }{
		{"nginx:1.25", "sha256:abc", "nginx@sha256:abc"},
		{"nginx", "sha256:abc", "nginx@sha256:abc"},
		{"ghcr.io/acme/app:v3", "sha256:abc", "ghcr.io/acme/app@sha256:abc"},
		{"registry.local:5000/app:v3", "sha256:abc", "registry.local:5000/app@sha256:abc"},
		{"registry.local:5000/app", "sha256:abc", "registry.local:5000/app@sha256:abc"},
		{"nginx:1.25", "not-a-digest", "nginx:1.25"}, // fallback
	}
	for _, c := range cases {
		if got := pinnedRef(c.ref, c.digest); got != c.want {
			t.Errorf("pinnedRef(%q, %q) = %q, want %q", c.ref, c.digest, got, c.want)
		}
	}
}

func TestRollbackFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// Deployment #1 succeeds and records artifacts.
	d1, err := e.deploy.Deploy(ctx, "app", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if final := e.runUntilFinished(t, d1.ID); final.Status != "succeeded" {
		t.Fatalf("d1 = %s", final.Status)
	}

	// Roll back to #1 → deployment #2 with pinned image override.
	d2, err := e.deploy.Rollback(ctx, "app", 1)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if d2.TriggeredBy != "rollback" || !d2.RollbackOf.Valid || d2.RollbackOf.Int64 != d1.ID {
		t.Errorf("d2 = %+v", d2)
	}
	if final := e.runUntilFinished(t, d2.ID); final.Status != "succeeded" {
		t.Fatalf("rollback deployment = %s, err = %s", final.Status, final.Error.String)
	}

	// The override file pins the recorded digest.
	data, err := e.agent.FS().ReadFile(ctx, "app", rollbackFile)
	if err != nil {
		t.Fatalf("rollback file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "nginx@sha256:") {
		t.Errorf("override not digest-pinned:\n%s", content)
	}

	// The rollback ran compose up with the override and skipped pull/sync.
	calls := strings.Join(e.agent.Calls, " | ")
	if strings.Count(calls, "compose.pull(app)") != 1 { // only from d1
		t.Errorf("rollback ran pull: %s", calls)
	}

	// Rolling back to a failed deployment is refused.
	e.agent.Fail["compose.up"] = context.DeadlineExceeded
	d3, _ := e.deploy.Deploy(ctx, "app", "manual")
	e.runUntilFinished(t, d3.ID)
	delete(e.agent.Fail, "compose.up")
	if _, err := e.deploy.Rollback(ctx, "app", d3.Number); err == nil {
		t.Error("rollback to failed deployment allowed")
	}
}

// A service built on the server has no registry digest, and its old image is
// usually gone once a newer build took the tag. Pinning it made Compose try to
// build under a digest tag and fail ("build tag cannot contain a digest").
func TestRollbackLeavesBuiltServicesUnpinned(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.agent.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Image: "nginx:alpine"},
		"api": {Image: "app-api:latest", Build: true},
	}}

	d1, err := e.deploy.Deploy(ctx, "app", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if final := e.runUntilFinished(t, d1.ID); final.Status != "succeeded" {
		t.Fatalf("d1 = %s", final.Status)
	}
	d2, err := e.deploy.Rollback(ctx, "app", d1.Number)
	if err != nil {
		t.Fatal(err)
	}
	if final := e.runUntilFinished(t, d2.ID); final.Status != "succeeded" {
		t.Fatalf("rollback = %s: %s", final.Status, final.Error.String)
	}

	data, err := e.agent.FS().ReadFile(ctx, "app", rollbackFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "nginx@sha256:") || strings.Contains(string(data), "api") {
		t.Errorf("override should pin web and leave api alone:\n%s", data)
	}
	if !strings.Contains(strings.Join(e.eventMessages(t, d2.ID), "\n"), "warn: api is built on this server") {
		t.Errorf("no warning that api keeps its current image: %v", e.eventMessages(t, d2.ID))
	}
	if n := strings.Count(strings.Join(e.agent.Calls, " | "), "compose.build(app)"); n != 1 {
		t.Errorf("builds = %d, want 1 (only the first deployment)", n)
	}

	// The rollback recorded the digests it ran, so it can be a target too.
	arts, err := e.q.ListDeploymentArtifacts(ctx, d2.ID)
	if err != nil || len(arts) != 2 {
		t.Fatalf("rollback artifacts = %+v, %v", arts, err)
	}
	if _, err := e.deploy.Rollback(ctx, "app", d2.Number); err != nil {
		t.Errorf("rolling back to a rollback: %v", err)
	}
}

// A Git project gets its earlier code back by checking out the recorded
// commit and building it, even when no service pulls an image.
func TestGitRollbackRebuildsTheCommit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.deploy.projects.Create(ctx, projects.CreateReq{
		Name: "site", Source: "git", GitRepo: "https://example.com/site.git", Branch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	e.agent.Statuses["site"] = []agent.ServiceStatus{{Service: "web", Name: "site-web-1", State: "running"}}
	e.agent.Resolved["site"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Build: true},
	}}

	const first, second = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	e.agent.GitCommit = first
	d1, err := e.deploy.Deploy(ctx, "site", "manual")
	if err != nil {
		t.Fatal(err)
	}
	e.runUntilFinished(t, d1.ID)
	e.agent.GitCommit = second
	d2, err := e.deploy.Deploy(ctx, "site", "manual")
	if err != nil {
		t.Fatal(err)
	}
	e.runUntilFinished(t, d2.ID)

	e.agent.Calls = nil
	d3, err := e.deploy.Rollback(ctx, "site", d1.Number)
	if err != nil {
		t.Fatalf("Rollback with no recorded digests: %v", err)
	}
	final := e.runUntilFinished(t, d3.ID)
	if final.Status != "succeeded" {
		t.Fatalf("rollback = %s: %s", final.Status, final.Error.String)
	}
	if final.GitCommit.String != first {
		t.Errorf("rollback synced %q, want the first commit", final.GitCommit.String)
	}
	calls := strings.Join(e.agent.Calls, " | ")
	for _, want := range []string{"host.gitsync(site)", "compose.build(site)", "compose.up(site)"} {
		if !strings.Contains(calls, want) {
			t.Errorf("rollback did not run %s: %s", want, calls)
		}
	}
	if strings.Contains(calls, "compose.pull(site)") {
		t.Errorf("rollback pulled: %s", calls)
	}
	data, err := e.agent.FS().ReadFile(ctx, "site", rollbackFile)
	if err != nil || strings.TrimSpace(string(data[strings.LastIndex(string(data), "services"):])) != "services: {}" {
		t.Errorf("override = %q, %v", data, err)
	}
}

// A deployment row that never got its job would count as active for ever,
// refusing every later deploy. Each step after the row is written can fail;
// the row must end failed so the project is not locked.
func TestQueueFailureDoesNotLockProject(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	d1, err := e.deploy.Deploy(ctx, "app", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if final := e.runUntilFinished(t, d1.ID); final.Status != "succeeded" {
		t.Fatalf("d1 = %s", final.Status)
	}

	triggers := map[string]string{
		"enqueue":  "CREATE TRIGGER t_fail BEFORE INSERT ON jobs BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END",
		"artifact": "CREATE TRIGGER t_fail BEFORE INSERT ON deployment_artifacts BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END",
	}
	for _, name := range []string{"enqueue", "artifact"} {
		if _, err := e.sqlDB.Exec(triggers[name]); err != nil {
			t.Fatal(err)
		}
		// Deploy writes no artifacts, so only the jobs insert can fail it.
		if name == "enqueue" {
			if _, err := e.deploy.Deploy(ctx, "app", "manual"); err == nil {
				t.Errorf("%s: Deploy succeeded with the jobs insert failing", name)
			}
		}
		_, rerr := e.deploy.Rollback(ctx, "app", d1.Number)
		if _, err := e.sqlDB.Exec("DROP TRIGGER t_fail"); err != nil {
			t.Fatal(err)
		}
		if rerr == nil || errors.Is(rerr, ErrDeployInProgress) {
			t.Errorf("%s: Rollback = %v, want the insert's error", name, rerr)
		}

		active, err := e.q.CountActiveDeployments(ctx, e.project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if active != 0 {
			t.Fatalf("%s: %d deployment(s) left active after a queue failure", name, active)
		}
	}

	d, err := e.deploy.Deploy(ctx, "app", "manual")
	if err != nil {
		t.Fatalf("deploy after queue failures: %v", err)
	}
	if final := e.runUntilFinished(t, d.ID); final.Status != "succeeded" {
		t.Fatalf("deploy after queue failures = %s", final.Status)
	}
}

// Git can be disconnected by editing .windlass.json after the target ran.
// The sync step then does nothing, so the log must not claim a rebuild at
// the target's commit.
func TestGitRollbackAfterDisconnectSaysSo(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.deploy.projects.Create(ctx, projects.CreateReq{
		Name: "site", Source: "git", GitRepo: "https://example.com/site.git", Branch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	e.agent.Statuses["site"] = []agent.ServiceStatus{{Service: "web", Name: "site-web-1", State: "running"}}
	e.agent.Resolved["site"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Build: true},
	}}
	e.agent.GitCommit = "1111111111111111111111111111111111111111"
	d1, err := e.deploy.Deploy(ctx, "site", "manual")
	if err != nil {
		t.Fatal(err)
	}
	e.runUntilFinished(t, d1.ID)

	if _, err := e.sqlDB.Exec(`UPDATE projects SET source = 'manual', git_repo = NULL WHERE name = 'site'`); err != nil {
		t.Fatal(err)
	}
	d2, err := e.deploy.Rollback(ctx, "site", d1.Number)
	if err != nil {
		t.Fatal(err)
	}
	if final := e.runUntilFinished(t, d2.ID); final.Status != "succeeded" {
		t.Fatalf("rollback = %s: %s", final.Status, final.Error.String)
	}
	events, err := e.q.ListDeploymentEvents(ctx, db.ListDeploymentEventsParams{DeploymentID: d2.ID, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var log []string
	for _, ev := range events {
		log = append(log, ev.Type+": "+ev.Message)
	}
	all := strings.Join(log, "\n")
	if strings.Contains(all, "rebuilt at commit") {
		t.Errorf("log claims a rebuild at the old commit:\n%s", all)
	}
	if !strings.Contains(all, "warn: web is built on this server and the project is no longer connected to Git") {
		t.Errorf("log does not say Git is disconnected:\n%s", all)
	}
}
