package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

// TestGitSyncLogsReplacedFiles covers the first sync of a src holding the
// user's own files and no repository. The checkout overwrites tracked paths,
// so each existing file it replaces must be named in the sink, and files the
// repository does not track must not be.
func TestGitSyncLogsReplacedFiles(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	work := filepath.Join(root, "work")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(gitBin, args...) //nolint:gosec // G204: the test drives git directly
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}
	git(work, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "compose.yaml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(work, "add", ".")
	git(work, "commit", "-q", "-m", "one")
	bare := filepath.Join(root, "app.git")
	git(root, "clone", "-q", "--bare", work, bare)

	projects := t.TempDir()
	l, err := New(Config{ProjectsDir: projects})
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(projects, "app", "src")
	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "compose.yaml"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "notes.txt"), []byte("untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var lines []string
	sink := func(l agent.LogLine) { lines = append(lines, l.Text) }
	req := agent.GitSyncReq{Project: "app", URL: bare, Branch: "main"}
	if _, err := (hostLocal{l}).GitSync(context.Background(), req, sink); err != nil {
		t.Fatalf("sync: %v", err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "checkout replaces existing file: compose.yaml") {
		t.Errorf("replaced tracked file not logged; log:\n%s", joined)
	}
	if strings.Contains(joined, "notes.txt") {
		t.Errorf("untracked file wrongly reported as replaced; log:\n%s", joined)
	}
}
