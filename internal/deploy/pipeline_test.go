package deploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/agent/fake"
	"github.com/windlass-dev/windlass/internal/events"
	"github.com/windlass-dev/windlass/internal/git"
	"github.com/windlass-dev/windlass/internal/jobs"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/secrets"
	"github.com/windlass-dev/windlass/internal/store"
	"github.com/windlass-dev/windlass/internal/store/db"
	"github.com/windlass-dev/windlass/migrations"
)

type env struct {
	q       *db.Queries
	agent   *fake.Fake
	deploy  *Service
	runner  *jobs.Runner
	project db.Project
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
	q := db.New(sqlDB)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, _ := secrets.New(bytes.Repeat([]byte{1}, 32))
	ag := fake.New()
	bus := events.NewBus()
	proj := projects.New(q, ag, box, bus, logger)
	gitSvc := git.New(q, box, logger)
	runner := jobs.NewRunner(q, logger)
	dep := New(q, ag, proj, gitSvc, runner, bus, logger)

	p, err := proj.Create(context.Background(), projects.CreateReq{Name: "app"})
	if err != nil {
		t.Fatal(err)
	}

	// Healthy single-service defaults; individual tests override.
	ag.Statuses["app"] = []agent.ServiceStatus{{Service: "web", Name: "app-web-1", State: "running"}}
	ag.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Image: "nginx:alpine"},
	}}

	return &env{q: q, agent: ag, deploy: dep, runner: runner, project: p}
}

// runUntilFinished drives the job runner until the deployment reaches a
// terminal status or the timeout hits.
func (e *env) runUntilFinished(t *testing.T, deploymentID int64) db.Deployment {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.runner.Run(ctx)

	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			d, _ := e.q.GetDeploymentByID(context.Background(), deploymentID)
			t.Fatalf("deployment did not finish; status=%s", d.Status)
		case <-time.After(20 * time.Millisecond):
		}
		d, err := e.q.GetDeploymentByID(context.Background(), deploymentID)
		if err != nil {
			t.Fatal(err)
		}
		switch d.Status {
		case "succeeded", "failed", "cancelled":
			return d
		}
	}
}

func (e *env) eventMessages(t *testing.T, deploymentID int64) []string {
	t.Helper()
	evs, err := e.deploy.Events(context.Background(), deploymentID, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(evs))
	lastSeq := int64(0)
	for _, ev := range evs {
		if ev.Seq <= lastSeq {
			t.Errorf("event seq not monotonic: %d after %d", ev.Seq, lastSeq)
		}
		lastSeq = ev.Seq
		out = append(out, ev.Type+": "+ev.Message)
	}
	return out
}

func TestDeployHappyPath(t *testing.T) {
	e := newEnv(t)

	d, err := e.deploy.Deploy(context.Background(), "app", "manual")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if d.Number != 1 {
		t.Errorf("number = %d, want 1", d.Number)
	}

	final := e.runUntilFinished(t, d.ID)
	if final.Status != "succeeded" {
		t.Fatalf("status = %s, error = %s", final.Status, final.Error.String)
	}

	// The pipeline ran the right agent calls in order.
	calls := strings.Join(e.agent.Calls, " | ")
	for _, want := range []string{"compose.config(app)", "compose.pull(app)", "compose.up(app)", "compose.ps(app)"} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing agent call %s in: %s", want, calls)
		}
	}

	// Artifacts recorded with digests for rollback.
	arts, err := e.q.ListDeploymentArtifacts(context.Background(), d.ID)
	if err != nil || len(arts) != 1 {
		t.Fatalf("artifacts = %d, %v", len(arts), err)
	}
	if arts[0].Service != "web" || !strings.HasPrefix(arts[0].ImageDigest, "sha256:") {
		t.Errorf("artifact = %+v", arts[0])
	}

	// Events exist with monotonic seq and a final done event.
	msgs := e.eventMessages(t, d.ID)
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1], "done: succeeded") {
		t.Errorf("events = %v", msgs)
	}

	// .env was rendered before deploy.
	if _, err := e.agent.FS().ReadFile(context.Background(), "app", ".env"); err != nil {
		t.Errorf(".env not rendered: %v", err)
	}
}

func TestDeployFailurePath(t *testing.T) {
	e := newEnv(t)
	e.agent.Fail["compose.pull"] = errors.New("registry unreachable")

	d, _ := e.deploy.Deploy(context.Background(), "app", "manual")
	final := e.runUntilFinished(t, d.ID)

	if final.Status != "failed" {
		t.Fatalf("status = %s, want failed", final.Status)
	}
	if !strings.Contains(final.Error.String, "registry unreachable") {
		t.Errorf("error = %q", final.Error.String)
	}
	msgs := strings.Join(e.eventMessages(t, d.ID), "\n")
	if !strings.Contains(msgs, "error: ") {
		t.Errorf("no error event recorded:\n%s", msgs)
	}
}

func TestDeployVerifyUnhealthy(t *testing.T) {
	e := newEnv(t)
	e.agent.Statuses["app"] = []agent.ServiceStatus{
		{Service: "web", State: "running", Health: "unhealthy"},
	}

	d, _ := e.deploy.Deploy(context.Background(), "app", "manual")
	final := e.runUntilFinished(t, d.ID)
	if final.Status != "failed" || !strings.Contains(final.Error.String, "unhealthy") {
		t.Fatalf("status = %s, error = %s", final.Status, final.Error.String)
	}
}

func TestDeployApplicationHealthCheck(t *testing.T) {
	e := newEnv(t)
	e.agent.Resolved["app"] = agent.ResolvedConfig{
		Services: map[string]agent.ResolvedService{"web": {Image: "nginx:alpine"}},
		HealthChecks: []agent.ApplicationHealthCheck{{
			Service: "web", URL: "https://app.example.com/health",
			ExpectedStatus: 200, Contains: "ready", StabilitySeconds: 0,
		}},
	}
	e.agent.HTTPResponses["https://app.example.com/health"] = agent.HTTPCheckResult{
		StatusCode: 200, Body: `{"status":"ready"}`,
	}

	d, _ := e.deploy.Deploy(context.Background(), "app", "manual")
	final := e.runUntilFinished(t, d.ID)
	if final.Status != "succeeded" {
		t.Fatalf("status = %s, error = %s", final.Status, final.Error.String)
	}
	if calls := strings.Join(e.agent.Calls, " | "); !strings.Contains(calls, "host.httpcheck(https://app.example.com/health)") {
		t.Fatalf("application health check was not executed: %s", calls)
	}
}

func TestDeployApplicationHealthCheckFailure(t *testing.T) {
	e := newEnv(t)
	oldTimeout, oldPoll := VerifyTimeout, verifyPollInterval
	VerifyTimeout, verifyPollInterval = 80*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { VerifyTimeout, verifyPollInterval = oldTimeout, oldPoll })
	e.agent.Resolved["app"] = agent.ResolvedConfig{
		Services: map[string]agent.ResolvedService{"web": {Image: "nginx:alpine"}},
		HealthChecks: []agent.ApplicationHealthCheck{{
			Service: "web", URL: "https://app.example.com/health",
			ExpectedStatus: 200,
		}},
	}
	e.agent.HTTPResponses["https://app.example.com/health"] = agent.HTTPCheckResult{StatusCode: 503}

	d, _ := e.deploy.Deploy(context.Background(), "app", "manual")
	final := e.runUntilFinished(t, d.ID)
	if final.Status != "failed" || !strings.Contains(final.Error.String, "returned HTTP 503") {
		t.Fatalf("status = %s, error = %s", final.Status, final.Error.String)
	}
}

// TestResumeAfterCrash simulates a process that died after checkpointing the
// "applying" step: the job row is 'running' with step=applying. On restart
// the runner reclaims it and the pipeline resumes at applying — without
// re-running sync or pull.
func TestResumeAfterCrash(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	d, err := e.q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ProjectID: e.project.ID, ProjectID_2: e.project.ID, TriggeredBy: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	e.q.MarkDeploymentStarted(ctx, db.MarkDeploymentStartedParams{Status: "applying", ID: d.ID})

	// The crashed process left the job mid-flight.
	job, err := e.q.EnqueueJob(ctx, db.EnqueueJobParams{
		Type: JobType, Payload: `{"deployment_id":` + itoa(d.ID) + `,"project":"app"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.ClaimNextJob(ctx); err != nil { // moves it to running
		t.Fatal(err)
	}
	e.q.CheckpointJob(ctx, db.CheckpointJobParams{Step: stepApplying, ID: job.ID})

	final := e.runUntilFinished(t, d.ID)
	if final.Status != "succeeded" {
		t.Fatalf("status = %s, error = %s", final.Status, final.Error.String)
	}

	calls := strings.Join(e.agent.Calls, " | ")
	if strings.Contains(calls, "compose.pull") || strings.Contains(calls, "host.gitsync") {
		t.Errorf("resume re-ran earlier steps: %s", calls)
	}
	if !strings.Contains(calls, "compose.up(app)") || !strings.Contains(calls, "compose.ps(app)") {
		t.Errorf("resume skipped applying/verifying: %s", calls)
	}
}

// TestSupersededDeploymentCancelled: a reclaimed job whose deployment is no
// longer the project's latest must cancel, not deploy stale state.
func TestSupersededDeploymentCancelled(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	old, _ := e.q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ProjectID: e.project.ID, ProjectID_2: e.project.ID, TriggeredBy: "manual",
	})
	if _, err := e.q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ProjectID: e.project.ID, ProjectID_2: e.project.ID, TriggeredBy: "manual",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := e.q.EnqueueJob(ctx, db.EnqueueJobParams{
		Type: JobType, Payload: `{"deployment_id":` + itoa(old.ID) + `,"project":"app"}`,
	}); err != nil {
		t.Fatal(err)
	}

	final := e.runUntilFinished(t, old.ID)
	if final.Status != "cancelled" {
		t.Fatalf("status = %s, want cancelled", final.Status)
	}
	if calls := strings.Join(e.agent.Calls, " | "); strings.Contains(calls, "compose.up") {
		t.Errorf("superseded deployment still deployed: %s", calls)
	}
}

func TestOneActiveDeploymentPerProject(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if _, err := e.deploy.Deploy(ctx, "app", "manual"); err != nil {
		t.Fatal(err)
	}
	// The first deployment is still queued (runner not started).
	if _, err := e.deploy.Deploy(ctx, "app", "manual"); !errors.Is(err, ErrDeployInProgress) {
		t.Fatalf("second deploy err = %v, want ErrDeployInProgress", err)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
