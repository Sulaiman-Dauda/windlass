package deploy

import (
	"context"
	"errors"
	"testing"
	"time"
)

// waitFor polls until cond holds or fails the test after 10 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (e *env) startRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = e.runner.Run(ctx) }()
}

func (e *env) countDeployments(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM deployments WHERE project_id = ?`, e.project.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeployWhileRunningWaitsThenStarts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	first, err := e.deploy.Deploy(ctx, "app", "manual")
	if err != nil {
		t.Fatal(err)
	}
	// The runner is not started, so the first deployment is still active.
	for i, trigger := range []string{"webhook", "manual"} {
		d, w, err := e.deploy.DeployOrQueue(ctx, "app", trigger)
		if err != nil {
			t.Fatal(err)
		}
		if w == nil || d.ID != 0 {
			t.Fatalf("request %d: want waiting, got deployment %+v", i+1, d)
		}
		if w.Requests != i+1 || w.Position != 1 || w.Trigger != "webhook" {
			t.Errorf("request %d: waiting = %+v", i+1, w)
		}
	}
	if n := e.countDeployments(t); n != 1 {
		t.Fatalf("%d deployments written, want 1: a waiting deploy has no row", n)
	}

	e.startRunner(t)
	waitFor(t, "the waiting deploy to start", func() bool { return e.countDeployments(t) == 2 })
	if w := e.deploy.WaitingFor("app"); w != nil {
		t.Errorf("still waiting after it started: %+v", w)
	}
	got, err := e.deploy.Get(ctx, "app", first.Number+1)
	if err != nil {
		t.Fatal(err)
	}
	if got.TriggeredBy != "webhook" {
		t.Errorf("started deploy trigger = %q, want the first request's", got.TriggeredBy)
	}
	waitFor(t, "the second deployment to finish", func() bool {
		d, _ := e.deploy.Get(ctx, "app", got.Number)
		return d.Status == "succeeded"
	})
	if first, _ := e.deploy.Get(ctx, "app", first.Number); first.Status != "succeeded" {
		t.Errorf("first deployment = %s, want succeeded, not superseded", first.Status)
	}
}

func TestCancelWaitingLeavesTheRunningDeploy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	first, err := e.deploy.Deploy(ctx, "app", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, w, err := e.deploy.DeployOrQueue(ctx, "app", "manual"); err != nil || w == nil {
		t.Fatalf("want waiting, got %v %v", w, err)
	}
	if _, err := e.deploy.CancelWaiting("app"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.deploy.CancelWaiting("app"); !errors.Is(err, ErrNothingWaiting) {
		t.Errorf("second cancel err = %v, want ErrNothingWaiting", err)
	}
	final := e.runUntilFinished(t, first.ID)
	if final.Status != "succeeded" {
		t.Errorf("running deployment = %s, want succeeded", final.Status)
	}
	time.Sleep(100 * time.Millisecond)
	if n := e.countDeployments(t); n != 1 {
		t.Errorf("%d deployments, want 1: the cancelled deploy must not start", n)
	}
}

func TestIdleDeployOrQueueDeploysAtOnce(t *testing.T) {
	e := newEnv(t)
	d, w, err := e.deploy.DeployOrQueue(context.Background(), "app", "manual")
	if err != nil || w != nil || d.Number != 1 {
		t.Fatalf("idle DeployOrQueue = %+v, %+v, %v", d, w, err)
	}
}

// On shutdown the job's context has ended: the waiting deploy is not
// started, and DropWaiting forgets it.
func TestStoppingKeepsWaitingForDropWaiting(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.deploy.Deploy(ctx, "app", "manual"); err != nil {
		t.Fatal(err)
	}
	if _, w, _ := e.deploy.DeployOrQueue(ctx, "app", "manual"); w == nil {
		t.Fatal("want waiting")
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	e.deploy.startWaiting(stopped, "app")
	if e.deploy.WaitingFor("app") == nil {
		t.Fatal("a stopping Windlass started or lost the waiting deploy")
	}
	e.deploy.DropWaiting("test")
	if e.deploy.WaitingFor("app") != nil {
		t.Error("DropWaiting kept it")
	}
}

// A waiting deploy starts after a failed run as well as a successful one.
func TestWaitingStartsAfterFailedRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.agent.Fail = map[string]error{"compose.up": errors.New("boom")}
	first, err := e.deploy.Deploy(ctx, "app", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, w, _ := e.deploy.DeployOrQueue(ctx, "app", "manual"); w == nil {
		t.Fatal("want waiting")
	}
	e.startRunner(t)
	waitFor(t, "the waiting deploy to start", func() bool { return e.countDeployments(t) == 2 })
	if d, _ := e.deploy.Get(ctx, "app", first.Number); d.Status != "failed" {
		t.Errorf("first = %s, want failed", d.Status)
	}
}

// A waiting deploy whose project was deleted is dropped, not retried.
func TestWaitingDroppedWhenItCannotStart(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.deploy.queueMu.Lock()
	e.deploy.waiting["gone"] = &Waiting{Project: "gone", Trigger: "manual", Requests: 1, Position: 1}
	e.deploy.queueMu.Unlock()
	e.deploy.startWaiting(ctx, "gone")
	if e.deploy.WaitingFor("gone") != nil {
		t.Error("a deploy that cannot start stayed waiting")
	}
}
