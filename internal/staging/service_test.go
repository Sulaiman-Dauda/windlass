package staging

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/agent/fake"
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

type recreateFixture struct {
	svc   *Service
	agent *fake.Fake
	q     *db.Queries
	sqlDB *sql.DB
	old   db.Project
	first db.Deployment
}

func newRecreateFixture(t *testing.T) recreateFixture {
	t.Helper()
	sqlDB, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := store.Migrate(sqlDB, migrations.FS); err != nil {
		t.Fatal(err)
	}
	q := db.New(sqlDB)
	ag := fake.New()
	ag.Files["app"] = map[string][]byte{
		"compose.yaml": []byte("services:\n  web:\n    image: nginx\n    environment:\n      URL: https://app.example.com\n"),
		".env":         []byte("VALUE=live\nNEW_LIVE_KEY=not-chosen-for-staging\n"),
		".windlass.json": []byte(`{"version":1,"source":"git","git_repo":"https://example.com/acme/app.git",
"git_branch":"main","domains":[{"hostname":"app.example.com","service":"web","container_port":80}]}`),
	}
	ag.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{"web": {Image: "nginx"}}}
	ag.Resolved["app-staging"] = ag.Resolved["app"]
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, err := secrets.New(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	proj := projects.New(q, ag, box, bus, logger)
	gitSvc := git.New(q, box, logger)
	runner := jobs.NewRunner(q, logger)
	dep := deploy.New(q, ag, proj, gitSvc, registries.New(q, box, logger), runner, bus, logger)
	px := proxy.New(q, ag, proj, bus, logger)
	svc := New(q, ag, proj, dep, gitSvc, px, logger)
	ctx := context.Background()
	if err := proj.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	copy, err := proj.CreateStagingCopy(ctx, projects.CopyReq{
		Live: "app", Branch: "preview", AutoPromote: true,
		Env:      map[string]string{"VALUE": "staging-edit", "EMPTY": "", "SMTP_HOST": "test-mail.invalid"},
		Rewrites: []projects.HostnameRewrite{{From: "app.example.com", To: "preview.example.com"}},
		// Actual domains can have changed since the original hostname mapping.
		Domains: []projects.DomainConfig{
			{Hostname: "changed-preview.example.com", Service: "web", ContainerPort: 8080},
			{Hostname: "extra-preview.example.com", Service: "web", ContainerPort: 80},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ProjectID: copy.Project.ID, ProjectID_2: copy.Project.ID, TriggeredBy: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.FinishDeployment(ctx, db.FinishDeploymentParams{ID: first.ID, Status: "failed",
		Error: sql.NullString{String: deploy.InterruptedDataCopyMessage, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	return recreateFixture{svc: svc, agent: ag, q: q, sqlDB: sqlDB, old: copy.Project, first: first}
}

func TestRecreatePreservesStagingSettings(t *testing.T) {
	e := newRecreateFixture(t)
	ctx := context.Background()
	beforeEnv, err := e.svc.projects.GetEnv(ctx, e.old.Name)
	if err != nil {
		t.Fatal(err)
	}
	beforeDomains, err := e.svc.projects.Domains(ctx, e.old.Name)
	if err != nil {
		t.Fatal(err)
	}
	_, beforeConfig, _, err := e.svc.projects.LiveFor(ctx, e.old.Name)
	if err != nil {
		t.Fatal(err)
	}
	status, err := e.svc.Status(ctx, e.old.Name)
	if err != nil || !status.CanRecreate {
		t.Fatalf("status = %+v, %v", status, err)
	}
	res, err := e.svc.Recreate(ctx, e.old.Name, true, "http://panel.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if res.Project.GitBranch != e.old.GitBranch || res.Project.GitRepo != e.old.GitRepo || res.Deployment != 1 {
		t.Fatalf("recreated project = %+v, first deployment = %d", res.Project, res.Deployment)
	}
	afterEnv, err := e.svc.projects.GetEnv(ctx, e.old.Name)
	if err != nil || !maps.Equal(beforeEnv, afterEnv) {
		t.Errorf("environment was not retained: %v", err)
	}
	afterDomains, err := e.svc.projects.Domains(ctx, e.old.Name)
	if err != nil || !reflect.DeepEqual(beforeDomains, afterDomains) {
		t.Errorf("domains = %+v, want %+v: %v", afterDomains, beforeDomains, err)
	}
	_, afterConfig, ok, err := e.svc.projects.LiveFor(ctx, e.old.Name)
	if err != nil || !ok || !reflect.DeepEqual(beforeConfig, afterConfig) {
		t.Errorf("staging settings = %+v, want %+v: %v", afterConfig, beforeConfig, err)
	}
	if !slices.Equal(e.agent.DownVolumes, []string{e.old.Name}) {
		t.Errorf("volumes removed from %v", e.agent.DownVolumes)
	}
	compose := string(e.agent.Files[e.old.Name]["compose.yaml"])
	if !strings.Contains(compose, "https://preview.example.com") {
		t.Errorf("saved hostname rewrites were lost: %s", compose)
	}
	var raw string
	if err := e.sqlDB.QueryRow(`SELECT payload FROM jobs WHERE type = 'deploy'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Project   string `json:"project"`
		Seed      string `json:"seed"`
		NoPromote bool   `json:"no_promote"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload.Project != e.old.Name || payload.Seed != "app" || !payload.NoPromote {
		t.Errorf("fresh data-copy job = %+v: %v", payload, err)
	}
	status, err = e.svc.Status(ctx, e.old.Name)
	if err != nil || status.CanRecreate {
		t.Errorf("queued replacement must not be recreatable: %+v, %v", status, err)
	}
}

func TestRecreateFailedDeleteKeepsOldStaging(t *testing.T) {
	for _, op := range []string{"compose.down", "fs.remove"} {
		t.Run(op, func(t *testing.T) {
			e := newRecreateFixture(t)
			before := maps.Clone(e.agent.Files[e.old.Name])
			callsBefore := len(e.agent.Calls)
			injected := errors.New("injected removal failure")
			e.agent.Fail[op] = injected
			_, err := e.svc.Recreate(context.Background(), e.old.Name, true, "")
			if !errors.Is(err, injected) {
				t.Fatalf("recreate error = %v", err)
			}
			if !reflect.DeepEqual(e.agent.Files[e.old.Name], before) {
				t.Fatal("failed delete changed staging's authoritative files")
			}
			p, err := e.q.GetProjectByName(context.Background(), e.old.Name)
			if err != nil || p.ID != e.old.ID {
				t.Errorf("old project was lost: %+v, %v", p, err)
			}
			if slices.Contains(e.agent.Calls[callsBefore:], "fs.copy(app,app-staging)") {
				t.Fatal("a replacement was copied after deletion failed")
			}
		})
	}
}

func TestRecreateOnlyInterruptedFirstCopy(t *testing.T) {
	for _, tc := range []struct {
		name, status, message string
		later                 bool
	}{
		{"succeeded", "succeeded", deploy.InterruptedDataCopyMessage, false},
		{"other failure", "failed", "application health check failed", false},
		{"no error", "failed", "", false},
		{"later interrupted deployment", "failed", deploy.InterruptedDataCopyMessage, true},
		{"later queued deployment", "queued", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRecreateFixture(t)
			ctx := context.Background()
			d := e.first
			if tc.later {
				var err error
				d, err = e.q.CreateDeployment(ctx, db.CreateDeploymentParams{
					ProjectID: e.old.ID, ProjectID_2: e.old.ID, TriggeredBy: "manual",
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := e.q.FinishDeployment(ctx, db.FinishDeploymentParams{ID: d.ID, Status: tc.status,
				Error: sql.NullString{String: tc.message, Valid: tc.message != ""}}); err != nil {
				t.Fatal(err)
			}
			st, err := e.svc.Status(ctx, e.old.Name)
			if err != nil || st.CanRecreate {
				t.Fatalf("ineligible status = %+v: %v", st, err)
			}
			if _, err := e.svc.Recreate(ctx, e.old.Name, true, ""); !errors.Is(err, ErrNotRecreatable) {
				t.Fatalf("ineligible recreate = %v", err)
			}
			if slices.Contains(e.agent.Calls, "compose.down(app-staging)") {
				t.Fatal("ineligible staging was taken down")
			}
		})
	}
}

func TestRecreateChecksBeforeDelete(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(recreateFixture)
		want    error
	}{
		{"acknowledgement", func(recreateFixture) {}, ErrNotAcknowledged},
		{"unreadable files", func(e recreateFixture) { e.agent.Fail["fs.read"] = errors.New("read failure") }, nil},
		{"shared volume", func(e recreateFixture) {
			cfg := e.agent.Resolved[e.old.Name]
			cfg.Volumes = map[string]agent.ResolvedVolume{"data": {Name: "app_data"}}
			e.agent.Resolved[e.old.Name] = cfg
		}, projects.ErrSharedVolume},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRecreateFixture(t)
			tc.prepare(e)
			_, err := e.svc.Recreate(context.Background(), e.old.Name, tc.name != "acknowledgement", "")
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("recreate error = %v, want %v", err, tc.want)
			}
			if slices.Contains(e.agent.Calls, "compose.down(app-staging)") {
				t.Fatal("delete ran before the settings and deletion checks passed")
			}
		})
	}
}

func TestRecreateRunsFreshCreatePreflight(t *testing.T) {
	e := newRecreateFixture(t)
	e.agent.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Image: "nginx", ContainerName: "fixed-live-container"},
	}}
	_, err := e.svc.Recreate(context.Background(), e.old.Name, true, "")
	if !errors.Is(err, ErrBlocked) || !strings.Contains(err.Error(), "container_name") {
		t.Fatalf("fresh isolation check = %v", err)
	}
	if _, exists := e.agent.Files[e.old.Name]; exists {
		t.Fatal("blocked create left a replacement behind")
	}
}

func TestDefaultHostname(t *testing.T) {
	cases := map[string]string{
		"example.com":        "staging.example.com",
		"example.co.uk":      "staging.example.co.uk",
		"app.example.com":    "staging-app.example.com",
		"shop.example.co.uk": "staging-shop.example.co.uk",
		"a.b.example.com":    "staging-a.b.example.com",
		"localhost":          "staging.localhost",
		"example.uk":         "staging.example.uk",
	}
	for in, want := range cases {
		if got := DefaultHostname(in); got != want {
			t.Errorf("DefaultHostname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{0: "0 B", 1023: "1023 B", 1 << 20: "1.0 MiB", 3 << 30: "3.0 GiB"}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestPromoteBlocked(t *testing.T) {
	staged := &DeploymentRef{Number: 2, Status: "succeeded", id: 12}
	cases := []struct {
		name string
		st   Status
		want string
	}{
		{"nothing to promote", Status{}, "staging has no succeeded deployment yet"},
		{"ready", Status{Promotable: staged, LiveCurrent: &DeploymentRef{Number: 1, id: 5}}, ""},
		{"staging deploying", Status{Promotable: staged, Latest: &DeploymentRef{Number: 3, Status: "building"}},
			"staging #3 is still deploying"},
		{"already promoted without Git", Status{Promotable: staged, LiveCurrent: &DeploymentRef{Number: 4, id: 20, promotedFrom: 12}},
			"live already runs staging #2"},
		{"live deployed the same commit", Status{
			Promotable:  &DeploymentRef{Number: 2, id: 12, Commit: "abc"},
			LiveCurrent: &DeploymentRef{Number: 4, id: 20, Commit: "abc"},
		}, "live already runs this commit"},
	}
	for _, c := range cases {
		if got := promoteBlocked(c.st); got != c.want {
			t.Errorf("%s: promoteBlocked = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestValuesOutsideEnv(t *testing.T) {
	cfg := agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		// From .env through ${STRIPE_SECRET_KEY}: the flagged list covers it.
		"web": {Environment: map[string]string{"STRIPE_SECRET_KEY": "sk_live_x", "PORT": "80"}},
		// From an env_file: same key as .env with another value, and one
		// .env does not have.
		"worker": {Environment: map[string]string{"STRIPE_SECRET_KEY": "sk_live_other", "SMTP_HOST": "mail.example.com"}},
	}}
	got := valuesOutsideEnv(cfg, map[string]string{"STRIPE_SECRET_KEY": "sk_live_x"})
	if len(got) != 1 || got[0].Service != "worker" || !strings.Contains(got[0].Message, "SMTP_HOST, STRIPE_SECRET_KEY") ||
		strings.Contains(got[0].Message, "sk_live") || strings.Contains(got[0].Message, "mail.example.com") {
		t.Fatalf("issues = %+v", got)
	}
}
