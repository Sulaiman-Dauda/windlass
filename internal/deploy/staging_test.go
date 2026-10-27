package deploy

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/agent/fake"
	"github.com/windlass-dev/windlass/internal/projects"
	"github.com/windlass-dev/windlass/internal/store/db"
)

const (
	tested   = "1111111111111111111111111111111111111111"
	untested = "2222222222222222222222222222222222222222"
)

// newGitStaging adds a Git project "site" with its staging "site-staging".
// Staging runs one service live does not have, as a mail catcher would.
func newGitStaging(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.deploy.projects.Create(ctx, projects.CreateReq{
		Name: "site", Source: "git", GitRepo: "https://example.com/site.git", Branch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.deploy.projects.CreateStagingCopy(ctx, projects.CopyReq{Live: "site"}); err != nil {
		t.Fatal(err)
	}
	e.agent.Resolved["site"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Image: "ghcr.io/acme/site:latest"},
	}}
	e.agent.Resolved["site-staging"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web":     {Image: "ghcr.io/acme/site:latest"},
		"mailpit": {Image: "axllent/mailpit"},
	}}
	e.agent.Statuses["site"] = []agent.ServiceStatus{{Service: "web", Name: "site-web-1", State: "running"}}
	e.agent.Statuses["site-staging"] = []agent.ServiceStatus{
		{Service: "web", Name: "site-staging-web-1", State: "running"},
		{Service: "mailpit", Name: "site-staging-mailpit-1", State: "running"},
	}
	return e
}

func (e *env) deployed(t *testing.T, project string) db.Deployment {
	t.Helper()
	d, err := e.deploy.Deploy(context.Background(), project, "manual")
	if err != nil {
		t.Fatal(err)
	}
	final := e.runUntilFinished(t, d.ID)
	if final.Status != "succeeded" {
		t.Fatalf("%s deploy = %s: %s", project, final.Status, final.Error.String)
	}
	return final
}

// runUntilIdle drives the runner until no job is queued or running, so work
// a job does after its deployment finishes has happened.
func (e *env) runUntilIdle(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = e.runner.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status IN ('queued','running')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("jobs did not finish")
}

func (e *env) deployments(t *testing.T, project string) []db.Deployment {
	t.Helper()
	list, err := e.deploy.List(context.Background(), project, 50)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestPromotePinsStagingsCommitAndDigests(t *testing.T) {
	e := newGitStaging(t)
	ctx := context.Background()
	e.agent.GitCommit = tested
	st := e.deployed(t, "site-staging")

	// Live's branch has moved on since staging tested its commit.
	e.agent.GitCommit = untested
	e.agent.Calls = nil
	d, err := e.deploy.Promote(ctx, "site-staging", st.Number, "manual")
	if err != nil {
		t.Fatal(err)
	}
	final := e.runUntilFinished(t, d.ID)
	if final.Status != "succeeded" {
		t.Fatalf("promotion = %s: %s", final.Status, final.Error.String)
	}
	if final.GitCommit.String != tested {
		t.Errorf("live deployed %q, want staging's commit", final.GitCommit.String)
	}
	if final.PromotedFrom.Int64 != st.ID {
		t.Errorf("promoted_from = %v, want %d", final.PromotedFrom, st.ID)
	}
	calls := strings.Join(e.agent.Calls, " | ")
	if !strings.Contains(calls, "host.gitsync(site)") || !strings.Contains(calls, "compose.up(site)") {
		t.Errorf("calls = %s", calls)
	}
	if strings.Contains(calls, "compose.pull(site)") {
		t.Errorf("promotion pulled: %s", calls)
	}

	// Only live's own services are pinned.
	arts, err := e.q.ListDeploymentArtifacts(ctx, final.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 || arts[0].Service != "web" {
		t.Fatalf("artifacts = %+v", arts)
	}
	override, err := e.agent.FS().ReadFile(ctx, "site", rollbackFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(override), "ghcr.io/acme/site@"+arts[0].ImageDigest) || strings.Contains(string(override), "mailpit") {
		t.Errorf("override = %s", override)
	}
}

func TestPromoteRefusals(t *testing.T) {
	e := newGitStaging(t)
	ctx := context.Background()

	if _, err := e.deploy.Promote(ctx, "site", 1, "manual"); !errors.Is(err, ErrNotPromotable) {
		t.Errorf("promote from live = %v", err)
	}

	e.agent.Fail["compose.up"] = errors.New("boom")
	bad, err := e.deploy.Deploy(ctx, "site-staging", "manual")
	if err != nil {
		t.Fatal(err)
	}
	e.runUntilFinished(t, bad.ID)
	delete(e.agent.Fail, "compose.up")
	if _, err := e.deploy.Promote(ctx, "site-staging", bad.Number, "manual"); !errors.Is(err, ErrNotPromotable) ||
		!strings.Contains(err.Error(), "only a succeeded deployment") {
		t.Errorf("promote failed deployment = %v", err)
	}

	good := e.deployed(t, "site-staging")
	if _, err := e.deploy.Deploy(ctx, "site", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.deploy.Promote(ctx, "site-staging", good.Number, "manual"); !errors.Is(err, ErrDeployInProgress) {
		t.Errorf("promote while live deploys = %v", err)
	}
	if n := len(e.deployments(t, "site")); n != 1 {
		t.Errorf("live has %d deployments, want only its own", n)
	}
}

func TestPromoteRefusesACommitOffLivesBranch(t *testing.T) {
	e := newGitStaging(t)
	ctx := context.Background()
	e.agent.GitCommit = untested
	st := e.deployed(t, "site-staging")
	e.agent.OffBranch[untested] = true

	e.agent.Calls = nil
	d, err := e.deploy.Promote(ctx, "site-staging", st.Number, "manual")
	if err != nil {
		t.Fatal(err)
	}
	final := e.runUntilFinished(t, d.ID)
	if final.Status != "failed" || !strings.Contains(final.Error.String, "commit 2222222 is not on main yet. Merge it, then promote") {
		t.Fatalf("promotion = %s: %s", final.Status, final.Error.String)
	}
	if slices.Contains(e.agent.Calls, "compose.up(site)") {
		t.Error("live was changed")
	}
}

func TestAutoPromote(t *testing.T) {
	e := newGitStaging(t)
	ctx := context.Background()
	if err := e.deploy.projects.SetAutoPromote(ctx, "site-staging", true); err != nil {
		t.Fatal(err)
	}

	// Staging's first deployment is a copy of live, never promoted.
	first, err := e.deploy.DeployFirst(ctx, "site-staging", "")
	if err != nil {
		t.Fatal(err)
	}
	if final := e.runUntilFinished(t, first.ID); final.Status != "succeeded" {
		t.Fatalf("first = %s: %s", final.Status, final.Error.String)
	}
	e.runUntilIdle(t)
	if n := len(e.deployments(t, "site")); n != 0 {
		t.Fatalf("first deployment promoted (%d live deployments)", n)
	}

	// A failed staging deployment is not promoted.
	e.agent.Fail["compose.up"] = errors.New("boom")
	bad, err := e.deploy.Deploy(ctx, "site-staging", "webhook")
	if err != nil {
		t.Fatal(err)
	}
	e.runUntilFinished(t, bad.ID)
	delete(e.agent.Fail, "compose.up")
	e.runUntilIdle(t)
	if n := len(e.deployments(t, "site")); n != 0 {
		t.Fatalf("failed deployment promoted (%d live deployments)", n)
	}

	st := e.deployed(t, "site-staging")
	e.runUntilIdle(t)
	live := e.deployments(t, "site")
	if len(live) != 1 || live[0].PromotedFrom.Int64 != st.ID || live[0].TriggeredBy != "webhook" {
		t.Fatalf("live deployments = %+v", live)
	}
	if live[0].Status != "succeeded" {
		t.Errorf("auto-promotion = %s: %s", live[0].Status, live[0].Error.String)
	}
	if msgs := strings.Join(e.eventMessages(t, st.ID), " | "); !strings.Contains(msgs, "auto-promoted to site as #1") {
		t.Errorf("staging events = %s", msgs)
	}

	// Rolling staging back is not promoted, or live would roll back with it.
	e.deployed(t, "site-staging")
	e.runUntilIdle(t)
	if n := len(e.deployments(t, "site")); n != 2 {
		t.Fatalf("second staging deployment not promoted (%d live deployments)", n)
	}
	rb, err := e.deploy.Rollback(ctx, "site-staging", st.Number)
	if err != nil {
		t.Fatal(err)
	}
	if final := e.runUntilFinished(t, rb.ID); final.Status != "succeeded" {
		t.Fatalf("rollback = %s: %s", final.Status, final.Error.String)
	}
	e.runUntilIdle(t)
	if n := len(e.deployments(t, "site")); n != 2 {
		t.Errorf("staging rollback promoted (%d live deployments)", n)
	}

	// With auto-promote off nothing follows.
	if err := e.deploy.projects.SetAutoPromote(ctx, "site-staging", false); err != nil {
		t.Fatal(err)
	}
	e.deployed(t, "site-staging")
	e.runUntilIdle(t)
	if n := len(e.deployments(t, "site")); n != 2 {
		t.Errorf("promoted with auto-promote off (%d live deployments)", n)
	}
}

func TestAutoPromoteRefusalIsRecorded(t *testing.T) {
	e := newGitStaging(t)
	ctx := context.Background()
	if err := e.deploy.projects.SetAutoPromote(ctx, "site-staging", true); err != nil {
		t.Fatal(err)
	}
	// Live has a deployment in flight, with no job to run it.
	live, err := e.deploy.projects.Get(ctx, "site")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ProjectID: live.ID, ProjectID_2: live.ID, TriggeredBy: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	st := e.deployed(t, "site-staging")
	e.runUntilIdle(t)
	if n := len(e.deployments(t, "site")); n != 1 {
		t.Errorf("live has %d deployments, want 1", n)
	}
	msgs := strings.Join(e.eventMessages(t, st.ID), " | ")
	if !strings.Contains(msgs, "auto-promote did not run") {
		t.Errorf("staging events = %s", msgs)
	}
}

// seedEnv gives the default "app" a database and an upload volume, and a
// staging copy with the same shape.
func seedEnv(t *testing.T, dump fake.ExecScript) *env {
	t.Helper()
	e := newEnv(t)
	if _, err := e.deploy.projects.CreateStagingCopy(context.Background(), projects.CopyReq{Live: "app"}); err != nil {
		t.Fatal(err)
	}
	shape := func(project string) agent.ResolvedConfig {
		return agent.ResolvedConfig{
			Volumes: map[string]agent.ResolvedVolume{
				"uploads": {Name: project + "_uploads"},
				"pgdata":  {Name: project + "_pgdata"},
				"fresh":   {Name: project + "_fresh"},
				"mydata":  {Name: project + "_mydata"},
			},
			Services: map[string]agent.ResolvedService{
				"web": {Image: "nginx:alpine", Mounts: []agent.Mount{
					{Type: "volume", Source: "uploads", Target: "/uploads"},
					{Type: "volume", Source: "fresh", Target: "/fresh"},
				}},
				"db": {Image: "postgres:16", Mounts: []agent.Mount{{Type: "volume", Source: "pgdata", Target: "/var/lib/postgresql/data"}}},
				// A second database: only one is dumped, and this one's files
				// are not copied while it runs.
				"legacy": {Image: "mariadb:11", Mounts: []agent.Mount{{Type: "volume", Source: "mydata", Target: "/var/lib/mysql"}}},
			},
		}
	}
	e.agent.Resolved["app"] = shape("app")
	e.agent.Resolved["app-staging"] = shape("app-staging")
	e.agent.Statuses["app-staging"] = []agent.ServiceStatus{
		{Service: "web", Name: "app-staging-web-1", State: "running"},
		{Service: "db", Name: "app-staging-db-1", State: "running"},
	}
	e.agent.Containers = []agent.Container{
		{ID: "live-db", ComposeProject: "app", ComposeService: "db", Image: "postgres:16", State: "running"},
		{ID: "stg-db", ComposeProject: "app-staging", ComposeService: "db", Image: "postgres:16", State: "running"},
	}
	// Live has made uploads and its database volume, but not fresh.
	e.agent.Volumes = map[string]int64{"app_uploads": 10 << 20, "app_pgdata": 50 << 20, "app_mydata": 5 << 20}
	e.agent.ExecScripts["live-db"] = dump
	e.agent.ExecScripts["stg-db"] = fake.ExecScript{}
	return e
}

func TestSeedCopiesVolumesAndStreamsTheDatabase(t *testing.T) {
	e := seedEnv(t, fake.ExecScript{Output: "CREATE TABLE orders ();\n"})
	ctx := context.Background()
	d, err := e.deploy.DeployFirst(ctx, "app-staging", "app")
	if err != nil {
		t.Fatal(err)
	}
	final := e.runUntilFinished(t, d.ID)
	if final.Status != "succeeded" {
		t.Fatalf("seeded deploy = %s: %s", final.Status, final.Error.String)
	}
	if !slices.Equal(e.agent.CopiedVolumes, []string{"app_uploads>app-staging_uploads"}) {
		t.Errorf("copied volumes = %v", e.agent.CopiedVolumes)
	}
	if got := string(e.agent.ExecInput["stg-db"]); got != "CREATE TABLE orders ();\n" {
		t.Errorf("staging's database received %q", got)
	}
	if cmds := strings.Join(e.agent.ExecCmds["live-db"], " | "); !strings.Contains(cmds, "pg_dump") {
		t.Errorf("live ran %s", cmds)
	}
	if cmds := strings.Join(e.agent.ExecCmds["stg-db"], " | "); !strings.Contains(cmds, "pg_isready") || !strings.Contains(cmds, "psql") {
		t.Errorf("staging ran %s", cmds)
	}

	// Volumes are made before anything starts, then the database starts alone.
	create := slices.Index(e.agent.Calls, "compose.create(app-staging)")
	firstUp := slices.Index(e.agent.Calls, "compose.up(app-staging)")
	if create < 0 || firstUp < create {
		t.Errorf("calls = %v", e.agent.Calls)
	}
	var ups []agent.ComposeUpReq
	for _, r := range e.agent.UpRequests {
		if r.Project == "app-staging" {
			ups = append(ups, r)
		}
	}
	if len(ups) != 2 || !slices.Equal(ups[0].Services, []string{"db"}) || !ups[0].NoDeps || len(ups[1].Services) != 0 {
		t.Errorf("staging up requests = %+v", ups)
	}
	msgs := strings.Join(e.eventMessages(t, d.ID), " | ")
	if !strings.Contains(msgs, "volume fresh: live has not made it yet") ||
		!strings.Contains(msgs, "volume mydata: belongs to database legacy") {
		t.Errorf("events = %s", msgs)
	}
	if slices.ContainsFunc(e.agent.Calls, func(c string) bool { return c == "compose.up(app)" || c == "compose.down(app)" }) {
		t.Error("live was changed")
	}
}

func TestSeedFailsWhenLivesDumpFails(t *testing.T) {
	e := seedEnv(t, fake.ExecScript{Output: "pg_dump: error: connection refused\n", Exit: 1})
	d, err := e.deploy.DeployFirst(context.Background(), "app-staging", "app")
	if err != nil {
		t.Fatal(err)
	}
	final := e.runUntilFinished(t, d.ID)
	if final.Status != "failed" || !strings.Contains(final.Error.String, "dump from live failed (exit 1)") {
		t.Fatalf("seeded deploy = %s: %s", final.Status, final.Error.String)
	}
}

func TestSeedNeedsLivesDatabaseRunning(t *testing.T) {
	e := seedEnv(t, fake.ExecScript{})
	e.agent.Containers[0].State = "exited"
	d, err := e.deploy.DeployFirst(context.Background(), "app-staging", "app")
	if err != nil {
		t.Fatal(err)
	}
	final := e.runUntilFinished(t, d.ID)
	if final.Status != "failed" || !strings.Contains(final.Error.String, "is not running, so it cannot be copied") {
		t.Fatalf("seeded deploy = %s: %s", final.Status, final.Error.String)
	}
}

func TestStagingDeployChecksIsolationEachTime(t *testing.T) {
	e := newGitStaging(t)
	cfg := e.agent.Resolved["site-staging"]
	web := cfg.Services["web"]
	web.ContainerName = "site-web"
	cfg.Services["web"] = web
	e.agent.Resolved["site-staging"] = cfg

	d, err := e.deploy.Deploy(context.Background(), "site-staging", "manual")
	if err != nil {
		t.Fatal(err)
	}
	final := e.runUntilFinished(t, d.ID)
	if final.Status != "failed" || !strings.Contains(final.Error.String, "staging would share with live") ||
		!strings.Contains(final.Error.String, "container_name") {
		t.Fatalf("deploy = %s: %s", final.Status, final.Error.String)
	}
	if slices.Contains(e.agent.Calls, "compose.up(site-staging)") {
		t.Error("staging started")
	}
}

// A seed cut off part way leaves staging half copied, so resuming it would
// start staging on a partial database. It fails and says what to do.
func TestInterruptedSeedFailsWithTheFix(t *testing.T) {
	e := seedEnv(t, fake.ExecScript{Output: "SELECT 1;\n"})
	ctx := context.Background()
	staging, err := e.deploy.projects.Get(ctx, "app-staging")
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ProjectID: staging.ID, ProjectID_2: staging.ID, TriggeredBy: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.q.MarkDeploymentStarted(ctx, db.MarkDeploymentStartedParams{Status: "applying", ID: d.ID}); err != nil {
		t.Fatal(err)
	}
	job, err := e.q.EnqueueJob(ctx, db.EnqueueJobParams{
		Type:    JobType,
		Payload: `{"deployment_id":` + itoa(d.ID) + `,"project":"app-staging","seed":"app","no_promote":true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.ClaimNextJob(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.q.CheckpointJob(ctx, db.CheckpointJobParams{Step: stepApplying, ID: job.ID}); err != nil {
		t.Fatal(err)
	}

	final := e.runUntilFinished(t, d.ID)
	if final.Status != "failed" || final.Error.String != "data copy was interrupted; delete staging and create it again" {
		t.Fatalf("resumed seed = %s: %s", final.Status, final.Error.String)
	}
	if slices.Contains(e.agent.Calls, "compose.up(app-staging)") || len(e.agent.CopiedVolumes) != 0 {
		t.Errorf("resume carried on: %v", e.agent.Calls)
	}
}
