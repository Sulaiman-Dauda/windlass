package projects

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/agent/fake"
)

func liveFiles() map[string][]byte {
	return map[string][]byte{
		"compose.yaml":          []byte("services:\n  web:\n    image: nginx\n    environment:\n      URL: https://app.example.com\n      API: https://api.app.example.com\n"),
		"compose.override.yaml": []byte("services:\n  web:\n    labels:\n      host: app.example.com\n"),
		"nginx.conf":            []byte("server_name app.example.com;\n"),
		".env":                  []byte("SECRET=live\n"),
		"compose.rollback.yaml": []byte("services: {}\n"),
		"src/main.go":           []byte("package main\n"),
		manifestFile: []byte(`{"version":1,"source":"git","git_repo":"https://example.com/acme/app.git",
"git_branch":"main","auto_deploy":true,"domains":[{"hostname":"app.example.com","service":"web","container_port":80}]}`),
	}
}

func newStagingFixture(t *testing.T) (*Service, *fake.Fake) {
	t.Helper()
	ag := fake.New()
	ag.Files["app"] = liveFiles()
	s, _ := testService(t, ag)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, ag
}

func TestCreateStagingCopy(t *testing.T) {
	ctx := context.Background()
	s, ag := newStagingFixture(t)
	res, err := s.CreateStagingCopy(ctx, CopyReq{
		Live:     "app",
		Rewrites: []HostnameRewrite{{From: "app.example.com", To: "staging-app.example.com"}},
		Env:      map[string]string{"SECRET": "staging"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Project.Name != "app-staging" || res.Project.StagingOf.String != "app" {
		t.Fatalf("project = %+v", res.Project)
	}
	files := ag.Files["app-staging"]
	for _, skipped := range []string{"src/main.go", "compose.rollback.yaml"} {
		if _, ok := files[skipped]; ok {
			t.Errorf("%s was copied", skipped)
		}
	}
	compose := string(files["compose.yaml"])
	if !strings.Contains(compose, "https://staging-app.example.com") || !strings.Contains(compose, "api.app.example.com") {
		t.Errorf("compose.yaml rewrite wrong:\n%s", compose)
	}
	if res.Rewritten["compose.yaml"] != 1 || res.Rewritten["compose.override.yaml"] != 1 {
		t.Errorf("rewritten = %v", res.Rewritten)
	}
	// Only compose files are rewritten; other files are the person's to check.
	if string(files["nginx.conf"]) != "server_name app.example.com;\n" {
		t.Errorf("nginx.conf changed: %s", files["nginx.conf"])
	}
	env := string(files[".env"])
	if !strings.Contains(env, "SECRET=staging") || !strings.Contains(env, "WINDLASS_ENVIRONMENT=staging") {
		t.Errorf(".env = %s", env)
	}

	m, err := s.readManifest(ctx, "app-staging")
	if err != nil {
		t.Fatal(err)
	}
	if m.Staging == nil || m.Staging.Of != "app" || m.Staging.AutoPromote || len(m.Staging.Rewrites) != 1 {
		t.Errorf("staging link = %+v", m.Staging)
	}
	if m.GitRepo != "https://example.com/acme/app.git" || m.GitBranch != "main" || !m.AutoDeploy || len(m.Domains) != 0 {
		t.Errorf("manifest = %+v", m)
	}

	staging, ok, err := s.StagingFor(ctx, "app")
	if err != nil || !ok || staging.Name != "app-staging" {
		t.Errorf("StagingFor = %v %v %v", staging.Name, ok, err)
	}
	live, cfg, ok, err := s.LiveFor(ctx, "app-staging")
	if err != nil || !ok || live.Name != "app" || cfg.Of != "app" {
		t.Errorf("LiveFor = %v %+v %v %v", live.Name, cfg, ok, err)
	}
	if _, err := s.CreateStagingCopy(ctx, CopyReq{Live: "app"}); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("second create = %v", err)
	}
}

func TestCreateStagingCopyUndoesAFailure(t *testing.T) {
	for _, op := range []string{"fs.copy", "fs.write"} {
		t.Run(op, func(t *testing.T) {
			ctx := context.Background()
			s, ag := newStagingFixture(t)
			ag.Fail[op] = errors.New("disk full")
			if _, err := s.CreateStagingCopy(ctx, CopyReq{Live: "app"}); err == nil {
				t.Fatal("create succeeded")
			}
			if _, ok := ag.Files["app-staging"]; ok {
				t.Error("staging directory left behind")
			}
			delete(ag.Fail, op)
			if _, err := s.Get(ctx, "app-staging"); !errors.Is(err, ErrNotFound) {
				t.Errorf("staging index left behind: %v", err)
			}
		})
	}
}

func TestStagingLinkSurvivesAFreshIndex(t *testing.T) {
	ctx := context.Background()
	s, ag := newStagingFixture(t)
	if _, err := s.CreateStagingCopy(ctx, CopyReq{Live: "app"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAutoPromote(ctx, "app-staging", true); err != nil {
		t.Fatal(err)
	}

	// A new database over the same files rebuilds the link.
	fresh, queries := testService(t, ag)
	if err := fresh.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := queries.GetProjectByName(ctx, "app-staging")
	if err != nil {
		t.Fatal(err)
	}
	if p.StagingOf.String != "app" {
		t.Errorf("staging_of = %q", p.StagingOf.String)
	}
	_, cfg, ok, err := fresh.LiveFor(ctx, "app-staging")
	if err != nil || !ok || !cfg.AutoPromote {
		t.Errorf("LiveFor after rebuild = %+v %v %v", cfg, ok, err)
	}
}

func TestStagingLinkNeedsTheName(t *testing.T) {
	ctx := context.Background()
	s, ag := newStagingFixture(t)
	// A manifest that claims to stage app under another name is not a link.
	ag.Files["other"] = map[string][]byte{
		"compose.yaml": []byte("services: {}\n"),
		manifestFile:   []byte(`{"version":1,"source":"manual","domains":[],"staging":{"of":"app"}}`),
	}
	if err := s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := s.LiveFor(ctx, "other"); ok {
		t.Error("other treated as app's staging")
	}
	if _, ok, _ := s.StagingFor(ctx, "app"); ok {
		t.Error("app has a staging")
	}
	if CanHaveStaging("app-staging") || CanHaveStaging(strings.Repeat("a", 56)) || !CanHaveStaging(strings.Repeat("a", 55)) {
		t.Error("CanHaveStaging limits wrong")
	}
}

func TestDeleteGuardsStaging(t *testing.T) {
	ctx := context.Background()
	s, ag := newStagingFixture(t)
	if _, err := s.CreateStagingCopy(ctx, CopyReq{Live: "app"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "app"); !errors.Is(err, ErrHasStaging) || !strings.Contains(err.Error(), "delete app-staging first") {
		t.Fatalf("delete live = %v", err)
	}
	if _, ok := ag.Files["app"]; !ok {
		t.Fatal("live removed")
	}

	// A staging whose down fails is kept, so its volumes are not orphaned.
	ag.Fail["compose.down"] = errors.New("docker unreachable")
	if err := s.Delete(ctx, "app-staging"); err == nil {
		t.Fatal("staging deleted though down failed")
	}
	delete(ag.Fail, "compose.down")

	if err := s.Delete(ctx, "app-staging"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ag.DownVolumes, []string{"app-staging"}) {
		t.Errorf("down -v ran for %v", ag.DownVolumes)
	}
	if err := s.Delete(ctx, "app"); err != nil {
		t.Fatalf("delete live after staging: %v", err)
	}
	if slices.Contains(ag.DownVolumes, "app") {
		t.Error("live's volumes were removed")
	}
}

func TestCheckIsolation(t *testing.T) {
	base := func() agent.ResolvedConfig {
		return agent.ResolvedConfig{
			WorkingDir: "/srv/projects/app",
			Volumes: map[string]agent.ResolvedVolume{
				"data":   {Name: "app_data"},
				"shared": {Name: "shared", External: true},
				"named":  {Name: "fixed-name"},
			},
			Networks: map[string]agent.ResolvedNetwork{
				"default": {Name: "app_default"},
				"edge":    {Name: "edge", External: true},
				"back":    {Name: "app_net"},
			},
			Services: map[string]agent.ResolvedService{"web": {Image: "nginx", Networks: []string{"default"},
				Mounts: []agent.Mount{
					{Type: "volume", Source: "data", Target: "/data"},
					{Type: "bind", Source: "/srv/projects/app/conf", Target: "/conf"},
				}}},
		}
	}
	if blockers, warnings := CheckIsolation(base(), "app"); len(blockers) != 0 || len(warnings) != 0 {
		t.Fatalf("clean config: %v %v", blockers, warnings)
	}

	cases := []struct {
		name   string
		change func(*agent.ResolvedService)
		want   string
	}{
		{"container_name", func(s *agent.ResolvedService) { s.ContainerName = "web" }, "container_name"},
		{"published port", func(s *agent.ResolvedService) {
			s.PublishedPorts = []agent.PublishedPort{{Target: 80, Published: "8080"}}
		}, "host port 8080"},
		{"host network", func(s *agent.ResolvedService) { s.NetworkMode = "host" }, "network_mode: host"},
		{"container network", func(s *agent.ResolvedService) { s.NetworkMode = "container:db" }, "container:db"},
		{"external network", func(s *agent.ResolvedService) { s.Networks = []string{"edge"} }, "external network edge"},
		{"named network", func(s *agent.ResolvedService) { s.Networks = []string{"back"} }, "network app_net"},
		{"external volume", func(s *agent.ResolvedService) {
			s.Mounts = []agent.Mount{{Type: "volume", Source: "shared"}}
		}, "external volume shared"},
		{"named volume", func(s *agent.ResolvedService) {
			s.Mounts = []agent.Mount{{Type: "volume", Source: "named"}}
		}, "fixed-name"},
		{"writable bind outside", func(s *agent.ResolvedService) {
			s.Mounts = []agent.Mount{{Type: "bind", Source: "/srv/uploads"}}
		}, "writes to /srv/uploads"},
		{"sibling with a shared prefix", func(s *agent.ResolvedService) {
			s.Mounts = []agent.Mount{{Type: "bind", Source: "/srv/projects/app-other"}}
		}, "writes to /srv/projects/app-other"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := base()
			svc := cfg.Services["web"]
			c.change(&svc)
			cfg.Services["web"] = svc
			blockers, _ := CheckIsolation(cfg, "app")
			if len(blockers) != 1 || !strings.Contains(blockers[0].Message, c.want) || blockers[0].Fix == "" {
				t.Errorf("blockers = %+v, want one containing %q", blockers, c.want)
			}
		})
	}

	cfg := base()
	svc := cfg.Services["web"]
	svc.Build, svc.Image = true, "ghcr.io/acme/app:latest"
	cfg.Services["web"] = svc
	if blockers, warnings := CheckIsolation(cfg, "app"); len(blockers) != 0 || len(warnings) != 1 ||
		!strings.Contains(warnings[0].Message, "ghcr.io/acme/app:latest") {
		t.Errorf("built image: %v %v", blockers, warnings)
	}
	svc.Image = "app-web"
	cfg.Services["web"] = svc
	if _, warnings := CheckIsolation(cfg, "app"); len(warnings) != 0 {
		t.Errorf("per-project image warned: %v", warnings)
	}

	cfg = base()
	svc = cfg.Services["web"]
	svc.Mounts = []agent.Mount{{Type: "bind", Source: "/etc/ssl/certs", ReadOnly: true}}
	cfg.Services["web"] = svc
	blockers, warnings := CheckIsolation(cfg, "app")
	if len(blockers) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0].Message, "read-only") {
		t.Errorf("read-only bind: %v %v", blockers, warnings)
	}
}

func TestRewriteHostnamesWholeNamesOnly(t *testing.T) {
	rw := []HostnameRewrite{{From: "app.example.com", To: "staging-app.example.com"}}
	cases := map[string]string{
		"https://app.example.com/path":        "https://staging-app.example.com/path",
		"APP=app.example.com":                 "APP=staging-app.example.com",
		"Visit app.example.com.":              "Visit staging-app.example.com.",
		"api.app.example.com":                 "api.app.example.com",
		"staging-app.example.com":             "staging-app.example.com",
		"app.example.com.evil":                "app.example.com.evil",
		"app.example.community":               "app.example.community",
		"myapp.example.com":                   "myapp.example.com",
		"a app.example.com b app.example.com": "a staging-app.example.com b staging-app.example.com",
	}
	for in, want := range cases {
		if got, _ := RewriteHostnames(in, rw); got != want {
			t.Errorf("RewriteHostnames(%q) = %q, want %q", in, got, want)
		}
	}
	// Rewriting staging's text again changes nothing.
	once, _ := RewriteHostnames("https://app.example.com", rw)
	if twice, n := RewriteHostnames(once, rw); twice != once || n != 0 {
		t.Errorf("second rewrite = %q (%d)", twice, n)
	}
}

func TestOutsideWorldKeys(t *testing.T) {
	got := OutsideWorldKeys(map[string]string{
		"STRIPE_SECRET_KEY": "", "SMTP_HOST": "", "MAIL_FROM": "", "aws-access-key": "",
		"DATABASE_URL": "", "APP_NAME": "", "MAILPIT": "", "SLACK_WEBHOOK_URL": "",
	})
	want := []string{"MAIL_FROM", "SLACK_WEBHOOK_URL", "SMTP_HOST", "STRIPE_SECRET_KEY", "aws-access-key"}
	if !slices.Equal(got, want) {
		t.Errorf("OutsideWorldKeys = %v, want %v", got, want)
	}
}

func TestHealthURLsOnHosts(t *testing.T) {
	cfg := agent.ResolvedConfig{HealthChecks: []agent.ApplicationHealthCheck{
		{Service: "web", URL: "https://APP.example.com/health"},
		{Service: "api", URL: "http://localhost:8080/health"},
	}}
	issues := HealthURLsOnHosts(cfg, []string{"app.example.com"})
	if len(issues) != 1 || issues[0].Service != "web" {
		t.Errorf("issues = %+v", issues)
	}
}
