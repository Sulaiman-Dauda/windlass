package local

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

// TestGitLogAndRemoteHead reads the commits between two deployed commits
// from a real checkout, and a branch head from a real remote.
func TestGitLogAndRemoteHead(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	work := filepath.Join(root, "work")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(gitBin, args...) //nolint:gosec // G204: the test drives git directly
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Ada", "GIT_AUTHOR_EMAIL=a@example.com",
			"GIT_COMMITTER_NAME=Ada", "GIT_COMMITTER_EMAIL=a@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	git(work, "init", "-q", "-b", "main")
	var commits []string
	for _, msg := range []string{"one", "two: with a | pipe", "three"} {
		git(work, "commit", "-q", "--allow-empty", "-m", msg)
		commits = append(commits, git(work, "rev-parse", "HEAD"))
	}
	bare := filepath.Join(root, "app.git")
	git(root, "clone", "-q", "--bare", work, bare)

	l, err := New(Config{ProjectsDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	h := hostLocal{l}
	ctx := context.Background()
	if _, err := h.GitSync(ctx, agent.GitSyncReq{Project: "app", URL: bare, Branch: "main"}, nil); err != nil {
		t.Fatal(err)
	}

	res, err := h.GitLog(ctx, agent.GitLogReq{Project: "app", From: commits[0], To: commits[2], Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for _, c := range res.Commits {
		subjects = append(subjects, c.Subject)
		if c.Author != "Ada" || c.Time.IsZero() || len(c.Hash) != 40 {
			t.Errorf("commit = %+v", c)
		}
	}
	if !slices.Equal(subjects, []string{"three", "two: with a | pipe"}) || res.More {
		t.Fatalf("log = %v, more = %v", subjects, res.More)
	}
	if res, err = h.GitLog(ctx, agent.GitLogReq{Project: "app", From: commits[0], To: commits[2], Limit: 1}); err != nil ||
		len(res.Commits) != 1 || !res.More {
		t.Fatalf("limited log = %+v, %v", res, err)
	}
	if res, err = h.GitLog(ctx, agent.GitLogReq{Project: "app", From: commits[2], To: commits[2]}); err != nil || len(res.Commits) != 0 {
		t.Fatalf("level log = %+v, %v", res, err)
	}
	missing := strings.Repeat("f", 40)
	if _, err := h.GitLog(ctx, agent.GitLogReq{Project: "app", From: missing, To: commits[2]}); !errors.Is(err, agent.ErrUnknownCommit) {
		t.Fatalf("unknown commit: %v", err)
	}
	if _, err := h.GitLog(ctx, agent.GitLogReq{Project: "app", From: "--all", To: commits[2]}); err == nil {
		t.Fatal("an option was accepted as a commit")
	}
	if _, err := h.GitLog(ctx, agent.GitLogReq{Project: "nocheckout", From: commits[0], To: commits[2]}); !errors.Is(err, agent.ErrUnknownCommit) {
		t.Fatalf("no checkout: %v", err)
	}

	// The remote moves on; the checkout does not.
	git(work, "commit", "-q", "--allow-empty", "-m", "four")
	git(work, "push", "-q", bare, "main")
	head, err := h.GitRemoteHead(ctx, agent.GitRemoteHeadReq{URL: bare, Branch: "main"})
	if err != nil || head != git(work, "rev-parse", "HEAD") {
		t.Fatalf("remote head = %q, %v", head, err)
	}
	if _, err := h.GitRemoteHead(ctx, agent.GitRemoteHeadReq{URL: bare, Branch: "nope"}); !errors.Is(err, agent.ErrBranchNotFound) {
		t.Fatalf("missing branch: %v", err)
	}
	if _, err := h.GitRemoteHead(ctx, agent.GitRemoteHeadReq{URL: filepath.Join(root, "missing.git"), Branch: "main"}); err == nil {
		t.Fatal("missing remote answered")
	}
	if _, err := h.GitRemoteHead(ctx, agent.GitRemoteHeadReq{URL: bare, Branch: "--upload-pack=x"}); err == nil {
		t.Fatal("an option was accepted as a branch")
	}
}
