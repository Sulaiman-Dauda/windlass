package attention

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/deploy"
)

type driftStub struct {
	mu    sync.Mutex
	by    map[string]deploy.Drift
	calls map[string]int
}

func (d *driftStub) Drift(_ context.Context, project string) (deploy.Drift, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls[project]++
	got, ok := d.by[project]
	if !ok {
		return deploy.Drift{}, errors.New("not a project")
	}
	return got, nil
}

func (d *driftStub) set(project string, drift deploy.Drift) {
	d.mu.Lock()
	d.by[project] = drift
	d.mu.Unlock()
}

func (e *env) sweepDone(t *testing.T) {
	t.Helper()
	waitFor(t, func() bool {
		e.svc.driftMu.Lock()
		defer e.svc.driftMu.Unlock()
		return !e.svc.driftRunning && !e.svc.driftExpiry.IsZero()
	})
}

func TestDriftRows(t *testing.T) {
	e := newEnv(t)
	stub := &driftStub{by: map[string]deploy.Drift{}, calls: map[string]int{}}
	e.svc.SetDrift(stub)
	e.agent.Containers = []agent.Container{
		{Name: "shop-web-1", ComposeProject: "shop", ComposeService: "web"},
		{Name: "shop-db-1", ComposeProject: "shop", ComposeService: "db"},
		{Name: "blog-web-1", ComposeProject: "blog", ComposeService: "web"},
		{Name: "stray", ComposeProject: "stray", ComposeService: "x"},
	}
	stub.set("shop", deploy.Drift{Project: "shop", Drifted: true, Services: []deploy.ServiceDrift{
		{Service: "db", ImageChanged: true},
		{Service: "web", ConfigChanged: true},
		{Service: "worker", Missing: true},
	}})
	stub.set("blog", deploy.Drift{Project: "blog", Services: []deploy.ServiceDrift{{Service: "web"}}})

	// The first list never waits for Compose.
	if items := e.list(t); len(items) != 0 {
		t.Fatalf("first list = %+v", items)
	}
	e.sweepDone(t)
	items := e.list(t)
	if kinds(items) != "drift:shop" {
		t.Fatalf("items = %s", kinds(items))
	}
	want := "web runs an older configuration; db runs an older image than the tag now names; " +
		"worker has no container. A deploy brings the containers in line."
	if it := items[0]; it.Title != "shop differs from its files" || it.Detail != want ||
		it.Link != "/projects/shop" || it.Tone != ToneInfo {
		t.Errorf("row = %+v", it)
	}
	if stub.calls["stray"] != 1 {
		t.Errorf("stray checked %d times", stub.calls["stray"])
	}

	// Cached until marked stale.
	e.list(t)
	if stub.calls["shop"] != 1 {
		t.Errorf("shop checked %d times while cached", stub.calls["shop"])
	}

	// A deploy fixes it: the next sweep clears the row.
	stub.set("shop", deploy.Drift{Project: "shop"})
	e.svc.DriftChanged()
	e.list(t)
	e.sweepDone(t)
	if items := e.list(t); len(items) != 0 {
		t.Errorf("after deploy = %s", kinds(items))
	}
}

func TestNoDriftCheckerNoRows(t *testing.T) {
	e := newEnv(t)
	e.agent.Containers = []agent.Container{{Name: "a", ComposeProject: "a", ComposeService: "a"}}
	if items := e.list(t); len(items) != 0 {
		t.Fatalf("items = %s", kinds(items))
	}
}

func TestDriftNames(t *testing.T) {
	for in, want := range map[int]string{1: "s0", 2: "s0 and s1", 3: "s0, s1 and s2", 5: "s0, s1, s2 and 2 more"} {
		list := make([]string, in)
		for i := range list {
			list[i] = "s" + string(rune('0'+i))
		}
		if got := names(list); got != want {
			t.Errorf("names(%d) = %q, want %q", in, got, want)
		}
	}
}
