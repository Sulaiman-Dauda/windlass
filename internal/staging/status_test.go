package staging

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/windlass-dev/windlass/internal/agent"
	"github.com/windlass-dev/windlass/internal/store/db"
)

// succeed records a succeeded deployment of project at commit.
func succeed(t *testing.T, q *db.Queries, project db.Project, commit string) {
	t.Helper()
	ctx := context.Background()
	d, err := q.CreateDeployment(ctx, db.CreateDeploymentParams{ProjectID: project.ID, ProjectID_2: project.ID, TriggeredBy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.SetDeploymentCommit(ctx, db.SetDeploymentCommitParams{GitCommit: sql.NullString{String: commit, Valid: true}, ID: d.ID}); err != nil {
		t.Fatal(err)
	}
	if err := q.FinishDeployment(ctx, db.FinishDeploymentParams{ID: d.ID, Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
}

func hash(c byte) string { return strings.Repeat(string(c), 40) }

func TestStatusCommitsToPromote(t *testing.T) {
	e := newRecreateFixture(t)
	ctx := context.Background()
	live, err := e.svc.projects.Get(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	// Newest first: d is staging's verified commit, a is live's.
	e.agent.History[e.old.Name] = []agent.GitCommit{
		{Hash: hash('d'), Subject: "Fix the basket total"}, {Hash: hash('c'), Subject: "Add VAT line"},
		{Hash: hash('b'), Subject: "Tidy"}, {Hash: hash('a'), Subject: "Live's commit"},
	}
	succeed(t, e.q, live, hash('a'))
	succeed(t, e.q, e.old, hash('d'))

	st, err := e.svc.Status(ctx, e.old.Name)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range st.Commits {
		got = append(got, c.Subject)
	}
	if !slices.Equal(got, []string{"Fix the basket total", "Add VAT line", "Tidy"}) || st.CommitsMore {
		t.Fatalf("commits = %v, more = %v", got, st.CommitsMore)
	}

	// Cached: the same range does not read the checkout again.
	calls := len(e.agent.Calls)
	if _, err := e.svc.Status(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(e.agent.Calls[calls:], func(c string) bool { return strings.HasPrefix(c, "host.gitlog") }) {
		t.Errorf("same range read again: %v", e.agent.Calls[calls:])
	}

	// Level: live runs staging's commit, so there is nothing to list.
	succeed(t, e.q, live, hash('d'))
	if st, err = e.svc.Status(ctx, "app"); err != nil || st.Commits == nil || len(st.Commits) != 0 {
		t.Fatalf("level: commits = %v, %v", st.Commits, err)
	}

	// Live's commit is not in staging's history (a force push): say nothing.
	succeed(t, e.q, live, hash('f'))
	if st, err = e.svc.Status(ctx, "app"); err != nil || st.Commits != nil {
		t.Fatalf("live commit missing: commits = %v, %v", st.Commits, err)
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), `"commits"`) {
		t.Errorf("commits field present: %s", raw)
	}

	// The unknown answer is kept for the range too.
	calls = len(e.agent.Calls)
	if st, err = e.svc.Status(ctx, "app"); err != nil || st.Commits != nil ||
		slices.ContainsFunc(e.agent.Calls[calls:], func(c string) bool { return strings.HasPrefix(c, "host.gitlog") }) {
		t.Fatalf("unknown range read again: %v, %v", e.agent.Calls[calls:], err)
	}

	// A checkout that cannot be read does not fail the card.
	e.agent.Fail["host.gitlog"] = errors.New("git broke")
	succeed(t, e.q, live, hash('b'))
	if st, err = e.svc.Status(ctx, "app"); err != nil || st.Commits != nil {
		t.Fatalf("git failure: commits = %v, %v", st.Commits, err)
	}
}

func TestStatusCommitsCapped(t *testing.T) {
	e := newRecreateFixture(t)
	ctx := context.Background()
	live, err := e.svc.projects.Get(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	var history []agent.GitCommit
	for i := range MaxPromoteCommits + 5 {
		history = append(history, agent.GitCommit{Hash: strings.Repeat("0", 38) + string("0123456789abcdef"[i/16]) + string("0123456789abcdef"[i%16])})
	}
	e.agent.History[e.old.Name] = history
	succeed(t, e.q, live, history[len(history)-1].Hash)
	succeed(t, e.q, e.old, history[0].Hash)
	st, err := e.svc.Status(ctx, "app")
	if err != nil || len(st.Commits) != MaxPromoteCommits || !st.CommitsMore {
		t.Fatalf("commits = %d, more = %v, %v", len(st.Commits), st.CommitsMore, err)
	}
}

func TestStatusBranchHead(t *testing.T) {
	e := newRecreateFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	e.svc.now = func() time.Time { return now }
	key := e.old.GitRepo.String + " preview"
	e.agent.RemoteHeads[key] = hash('e')
	asks := func() int {
		n := 0
		for _, c := range e.agent.Calls {
			if strings.HasPrefix(c, "host.gitremotehead") {
				n++
			}
		}
		return n
	}
	// status answers at once, then waits for the ask it started.
	status := func() Status {
		t.Helper()
		st, err := e.svc.Status(ctx, "app")
		if err != nil {
			t.Fatal(err)
		}
		e.svc.asks.Wait()
		return st
	}

	// The first poll does not wait on the remote; the next has the answer.
	if st := status(); st.BranchHead != "" || asks() != 1 {
		t.Fatalf("first poll: head = %q after %d asks", st.BranchHead, asks())
	}
	if st := status(); st.BranchHead != hash('e') || asks() != 1 {
		t.Fatalf("second poll: head = %q after %d asks", st.BranchHead, asks())
	}

	// Within the minute the remote is not asked again, even though it moved.
	e.agent.RemoteHeads[key] = hash('f')
	now = now.Add(BranchHeadTTL - time.Second)
	if st := status(); st.BranchHead != hash('e') || asks() != 1 {
		t.Fatalf("cached: head = %q after %d asks", st.BranchHead, asks())
	}
	// After it, the old head is served while one ask refreshes it.
	now = now.Add(2 * time.Second)
	if st := status(); st.BranchHead != hash('e') || asks() != 2 {
		t.Fatalf("expired: head = %q after %d asks", st.BranchHead, asks())
	}
	if st := status(); st.BranchHead != hash('f') {
		t.Fatalf("refreshed: head = %q", st.BranchHead)
	}

	// Unreachable: the field goes, the card still loads, and the failure is
	// kept for the minute too.
	e.agent.Fail["host.gitremotehead"] = errors.New("dial tcp: i/o timeout")
	now = now.Add(BranchHeadTTL)
	status()
	if st := status(); st.BranchHead != "" || asks() != 3 {
		t.Fatalf("unreachable: head = %q after %d asks", st.BranchHead, asks())
	}
}

// TestStatusBranchHeadOneAskAtATime: polls that arrive while the remote is
// slow share the one ask instead of each starting another.
func TestStatusBranchHeadOneAskAtATime(t *testing.T) {
	e := newRecreateFixture(t)
	ctx := context.Background()
	e.agent.RemoteHeads[e.old.GitRepo.String+" preview"] = hash('e')
	e.svc.mu.Lock()
	e.svc.heads[e.old.Name] = headEntry{key: e.old.GitRepo.String + " preview", asking: true}
	e.svc.mu.Unlock()
	for range 5 {
		if _, err := e.svc.Status(ctx, "app"); err != nil {
			t.Fatal(err)
		}
	}
	e.svc.asks.Wait()
	if slices.ContainsFunc(e.agent.Calls, func(c string) bool { return strings.HasPrefix(c, "host.gitremotehead") }) {
		t.Fatalf("polls during an ask asked again: %v", e.agent.Calls)
	}
}

func TestStatusEnvMissing(t *testing.T) {
	e := newRecreateFixture(t)
	st, err := e.svc.Status(context.Background(), e.old.Name)
	if err != nil {
		t.Fatal(err)
	}
	// Staging has VALUE, EMPTY, SMTP_HOST and WINDLASS_ENVIRONMENT; live has
	// VALUE and NEW_LIVE_KEY. A key live has and staging lacks is no risk.
	if !slices.Equal(st.EnvMissing, []string{"EMPTY", "SMTP_HOST"}) {
		t.Fatalf("env_missing = %v", st.EnvMissing)
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"test-mail.invalid", "staging-edit", "not-chosen-for-staging"} {
		if strings.Contains(string(raw), value) {
			t.Errorf("status carries the value %q: %s", value, raw)
		}
	}

	// None missing.
	e.agent.Files["app"][".env"] = []byte("VALUE=live\nEMPTY=\nSMTP_HOST=mail.example.com\n")
	st, err = e.svc.Status(context.Background(), "app")
	if err != nil || len(st.EnvMissing) != 0 {
		t.Fatalf("none missing: %v, %v", st.EnvMissing, err)
	}

	// A .env the parser refuses leaves the list out and the card loads.
	e.agent.Files["app"][".env"] = []byte("VALUE=live\n-----BEGIN PRIVATE KEY-----\n")
	st, err = e.svc.Status(context.Background(), "app")
	if err != nil || st.EnvMissing != nil {
		t.Fatalf("unreadable .env: %v, %v", st.EnvMissing, err)
	}
}
