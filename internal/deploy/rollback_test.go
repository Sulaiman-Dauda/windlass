package deploy

import (
	"context"
	"strings"
	"testing"
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
