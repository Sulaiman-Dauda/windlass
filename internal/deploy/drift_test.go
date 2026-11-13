package deploy

import (
	"context"
	"errors"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/projects"
)

func TestDriftNoneWhenContainersMatch(t *testing.T) {
	e := newEnv(t)
	e.agent.Hashes["app"] = map[string]string{"web": "h1"}
	e.agent.ImageIDs["nginx:alpine"] = "sha256:img1"
	e.agent.Containers = []agent.Container{{ID: "c1", Name: "app-web-1", ComposeProject: "app",
		ComposeService: "web", ConfigHash: "h1", ImageID: "sha256:img1"}}

	d, err := e.deploy.Drift(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if d.Drifted || len(d.Services) != 1 || d.Services[0].ConfigChanged || d.Services[0].ImageChanged {
		t.Fatalf("drift = %+v", d)
	}
	if len(d.Files) != 0 || len(e.agent.HashFiles["app"]) != 0 {
		t.Errorf("no deploy yet, so no extra files: %v %v", d.Files, e.agent.HashFiles["app"])
	}
}

func TestDriftConfigImageMissingAndOrphan(t *testing.T) {
	e := newEnv(t)
	e.agent.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{
		"web": {Image: "nginx:alpine"}, "worker": {Image: "busybox"}, "db": {Image: "postgres:17"},
	}}
	e.agent.Hashes["app"] = map[string]string{"web": "h1", "worker": "h2", "db": "h3"}
	// web: compose file changed since; worker: a pull moved the tag; db: no
	// container; old: removed from the file.
	e.agent.ImageIDs["nginx:alpine"] = "sha256:img1"
	e.agent.ImageIDs["busybox"] = "sha256:new"
	e.agent.Containers = []agent.Container{
		{ID: "c1", Name: "app-web-1", ComposeProject: "app", ComposeService: "web", ConfigHash: "old", ImageID: "sha256:img1"},
		{ID: "c2", Name: "app-worker-1", ComposeProject: "app", ComposeService: "worker", ConfigHash: "h2", ImageID: "sha256:old"},
		{ID: "c3", Name: "app-old-1", ComposeProject: "app", ComposeService: "old", ConfigHash: "x"},
		{ID: "c4", Name: "other-web-1", ComposeProject: "other", ComposeService: "web", ConfigHash: "x"},
	}

	d, err := e.deploy.Drift(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Drifted {
		t.Fatal("want drift")
	}
	got := map[string]ServiceDrift{}
	for _, s := range d.Services {
		got[s.Service] = s
	}
	if len(got) != 4 {
		t.Fatalf("services = %+v", d.Services)
	}
	if s := got["web"]; !s.ConfigChanged || s.ImageChanged {
		t.Errorf("web = %+v", s)
	}
	if s := got["worker"]; s.ConfigChanged || !s.ImageChanged {
		t.Errorf("worker = %+v", s)
	}
	if s := got["db"]; !s.Missing || s.Container != "" {
		t.Errorf("db = %+v", s)
	}
	if s := got["old"]; !s.Orphan || s.ConfigChanged {
		t.Errorf("old = %+v", s)
	}
}

func TestDriftAfterRollbackHashesTheOverride(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	d1, err := e.deploy.Deploy(ctx, "app", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if final := e.runUntilFinished(t, d1.ID); final.Status != "succeeded" {
		t.Fatalf("d1 = %s", final.Status)
	}
	if _, err := e.deploy.Drift(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if files := e.agent.HashFiles["app"]; len(files) != 0 {
		t.Fatalf("a plain deploy is hashed without overrides, got %v", files)
	}

	d2, err := e.deploy.Rollback(ctx, "app", 1)
	if err != nil {
		t.Fatal(err)
	}
	if final := e.runUntilFinished(t, d2.ID); final.Status != "succeeded" {
		t.Fatalf("rollback = %s", final.Status)
	}
	d, err := e.deploy.Drift(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if files := e.agent.HashFiles["app"]; len(files) != 1 || files[0] != rollbackFile {
		t.Errorf("a live rollback must be hashed with %s, got %v", rollbackFile, files)
	}
	if len(d.Files) != 1 {
		t.Errorf("response files = %v", d.Files)
	}

	// The rollback runs the old image by digest while the tag in the files
	// has moved on: that is the rollback working, not drift.
	e.agent.Resolved["app"] = agent.ResolvedConfig{Services: map[string]agent.ResolvedService{"web": {Image: "app:latest"}}}
	e.agent.Hashes["app"] = map[string]string{"web": "h1"}
	e.agent.ImageIDs["app:latest"] = "sha256:new"
	e.agent.Containers = []agent.Container{{ID: "c1", Name: "app-web-1", ComposeProject: "app",
		ComposeService: "web", ConfigHash: "h1", ImageID: "sha256:old"}}
	if d, err = e.deploy.Drift(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if d.Drifted {
		t.Errorf("a live rollback read as drift: %+v", d.Services)
	}
}

func TestDriftUnknownProject(t *testing.T) {
	e := newEnv(t)
	if _, err := e.deploy.Drift(context.Background(), "nope"); !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestDriftComposeErrorIsReturned(t *testing.T) {
	e := newEnv(t)
	e.agent.Fail["compose.hashes"] = errors.New("compose file invalid: boom")
	if _, err := e.deploy.Drift(context.Background(), "app"); err == nil {
		t.Fatal("want the compose error")
	}
}
