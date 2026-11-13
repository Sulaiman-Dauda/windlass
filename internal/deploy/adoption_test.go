package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/projects"
)

// A stack started by hand in the app directory under another project name.
func handRunStack() []agent.Container {
	return []agent.Container{{
		ID: "c1", Name: "legacy-db-1", State: "running",
		ComposeProject: "legacy", ComposeService: "db",
		ComposeWorkingDir: "/opt/windlass/projects/app",
	}}
}

func TestFirstDeployRefusedWhenStackRunsUnderAnotherName(t *testing.T) {
	e := newEnv(t)
	e.agent.Containers = handRunStack()
	e.agent.Resolved["app"] = agent.ResolvedConfig{
		Services: map[string]agent.ResolvedService{"db": {Image: "postgres:16"}},
		Volumes:  map[string]agent.ResolvedVolume{"data": {Name: "app_data"}},
	}

	_, err := e.deploy.Deploy(context.Background(), "app", "manual")
	var adoption *projects.AdoptionError
	if !errors.As(err, &adoption) {
		t.Fatalf("err = %v, want an AdoptionError", err)
	}
	c := adoption.Conflict
	if c.RunningAs != "legacy" || c.Project != "app" || len(c.Containers) != 1 {
		t.Fatalf("conflict = %+v", c)
	}
	msg := err.Error()
	for _, want := range []string{`"legacy"`, "app_data", "rename the directory to legacy",
		"docker compose -p legacy down"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
	list, err := e.deploy.List(context.Background(), "app", 10)
	if err != nil || len(list) != 0 {
		t.Fatalf("a refused deploy left %d rows (err %v)", len(list), err)
	}
}

func TestFirstDeployAllowedWhenNamesMatch(t *testing.T) {
	e := newEnv(t)
	e.agent.Containers = []agent.Container{{Name: "app-web-1", ComposeProject: "app",
		ComposeWorkingDir: "/opt/windlass/projects/app"}}
	d, err := e.deploy.Deploy(context.Background(), "app", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if got := e.runUntilFinished(t, d.ID); got.Status != "succeeded" {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestGuardOnlyAppliesBeforeTheFirstSuccess(t *testing.T) {
	e := newEnv(t)
	d, err := e.deploy.Deploy(context.Background(), "app", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if got := e.runUntilFinished(t, d.ID); got.Status != "succeeded" {
		t.Fatalf("status = %s", got.Status)
	}
	// A leftover stack appearing later does not block a project Windlass
	// already runs.
	e.agent.Containers = handRunStack()
	if _, err := e.deploy.Deploy(context.Background(), "app", "manual"); err != nil {
		t.Fatalf("second deploy refused: %v", err)
	}
}

func TestGuardStillAppliesAfterAFailedFirstDeploy(t *testing.T) {
	e := newEnv(t)
	e.agent.Fail["compose.pull"] = errors.New("pull failed")
	d, err := e.deploy.Deploy(context.Background(), "app", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if got := e.runUntilFinished(t, d.ID); got.Status != "failed" {
		t.Fatalf("status = %s", got.Status)
	}
	e.agent.Containers = handRunStack()
	var adoption *projects.AdoptionError
	if _, err := e.deploy.Deploy(context.Background(), "app", "manual"); !errors.As(err, &adoption) {
		t.Fatalf("err = %v, want an AdoptionError", err)
	}
}

func TestStackCopiedFromElsewhereIsAConflict(t *testing.T) {
	e := newEnv(t)
	e.agent.Containers = []agent.Container{{Name: "web-1", ComposeProject: "oldname",
		ComposeWorkingDir: "/srv/app/"}}
	var adoption *projects.AdoptionError
	if _, err := e.deploy.Deploy(context.Background(), "app", "manual"); !errors.As(err, &adoption) {
		t.Fatalf("err = %v, want an AdoptionError", err)
	}
	if !strings.Contains(adoption.Error(), "/srv/app/") {
		t.Errorf("message does not name the directory: %s", adoption.Error())
	}
}

// After the directory is renamed to adopt a stack, Compose keeps the old
// working_dir label. A new project with the old name must not be blocked.
func TestStackAlreadyAdoptedIsNotAConflict(t *testing.T) {
	e := newEnv(t)
	if _, err := e.deploy.projects.Create(context.Background(), projects.CreateReq{Name: "legacy"}); err != nil {
		t.Fatal(err)
	}
	e.agent.Containers = handRunStack()
	d, err := e.deploy.Deploy(context.Background(), "app", "manual")
	if err != nil {
		t.Fatalf("deploy refused: %v", err)
	}
	if got := e.runUntilFinished(t, d.ID); got.Status != "succeeded" {
		t.Fatalf("status = %s", got.Status)
	}
}
