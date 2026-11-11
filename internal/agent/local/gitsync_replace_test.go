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

// replaceRepo makes a bare repository on main with the given files committed
// and returns its path.
func replaceRepo(t *testing.T, files map[string]string) string {
	t.Helper()
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
	for name, body := range files {
		p := filepath.Join(work, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git(work, "add", ".")
	git(work, "commit", "-q", "-m", "one")
	bare := filepath.Join(root, "app.git")
	git(root, "clone", "-q", "--bare", work, bare)
	return bare
}

// replaceSync makes a project whose src holds what setup writes and no
// repository, runs a first sync from bare, and returns the log lines.
func replaceSync(t *testing.T, bare string, setup func(src string)) []string {
	t.Helper()
	projects := t.TempDir()
	l, err := New(Config{ProjectsDir: projects})
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(projects, "app", "src")
	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatal(err)
	}
	setup(src)
	var lines []string
	sink := func(l agent.LogLine) { lines = append(lines, l.Text) }
	req := agent.GitSyncReq{Project: "app", URL: bare, Branch: "main"}
	if _, err := (hostLocal{l}).GitSync(context.Background(), req, sink); err != nil {
		t.Fatalf("sync: %v", err)
	}
	return lines
}

// hasLine reports whether one of lines is exactly want.
func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// writeReplaceFile writes body to rel under dir, creating folders as needed.
func writeReplaceFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestGitSyncLogsReplacedFiles covers the first sync of a src holding the
// user's own files and no repository. The checkout overwrites tracked paths,
// so each existing file it replaces must be named in the sink, and files the
// repository does not track must not be.
func TestGitSyncLogsReplacedFiles(t *testing.T) {
	bare := replaceRepo(t, map[string]string{"compose.yaml": "services: {}\n"})
	lines := replaceSync(t, bare, func(src string) {
		writeReplaceFile(t, src, "compose.yaml", "mine\n")
		writeReplaceFile(t, src, "notes.txt", "untracked\n")
	})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "checkout replaces existing file: compose.yaml") {
		t.Errorf("replaced tracked file not logged; log:\n%s", joined)
	}
	if strings.Contains(joined, "notes.txt") {
		t.Errorf("untracked file wrongly reported as replaced; log:\n%s", joined)
	}
}

// TestGitSyncReplaceShapes covers the shapes of src that the checkout
// replaces without the file itself being a plain file that differs.
func TestGitSyncReplaceShapes(t *testing.T) {
	const reported = "checkout replaces existing file: "

	t.Run("folder where the repo has a file", func(t *testing.T) {
		bare := replaceRepo(t, map[string]string{"docs": "a doc\n"})
		lines := replaceSync(t, bare, func(src string) {
			writeReplaceFile(t, src, "docs/user.txt", "mine\n")
		})
		if joined := strings.Join(lines, "\n"); !strings.Contains(joined, reported+"docs") {
			t.Errorf("folder in the way of a tracked file not logged; log:\n%s", joined)
		}
	})

	t.Run("file where the repo has a folder", func(t *testing.T) {
		bare := replaceRepo(t, map[string]string{"lib/b": "b\n"})
		lines := replaceSync(t, bare, func(src string) {
			writeReplaceFile(t, src, "lib", "mine\n")
		})
		if joined := strings.Join(lines, "\n"); !strings.Contains(joined, reported+"lib") {
			t.Errorf("file in the way of a tracked folder not logged; log:\n%s", joined)
		}
	})

	t.Run("symlink where the repo has a folder", func(t *testing.T) {
		bare := replaceRepo(t, map[string]string{"assets/logo.png": "png\n"})
		outside := t.TempDir()
		writeReplaceFile(t, outside, "logo.png", "mine\n")
		lines := replaceSync(t, bare, func(src string) {
			if err := os.Symlink(outside, filepath.Join(src, "assets")); err != nil {
				t.Skipf("cannot create symlinks here: %v", err)
			}
		})
		joined := strings.Join(lines, "\n")
		if !hasLine(lines, reported+"assets") {
			t.Errorf("symlink in the way of a tracked folder not logged; log:\n%s", joined)
		}
		if strings.Contains(joined, "assets/logo.png") {
			t.Errorf("path behind a symlink wrongly reported; log:\n%s", joined)
		}
	})

	t.Run("identical file is not reported", func(t *testing.T) {
		bare := replaceRepo(t, map[string]string{"compose.yaml": "services: {}\n", "README.md": "hi\n"})
		lines := replaceSync(t, bare, func(src string) {
			writeReplaceFile(t, src, "compose.yaml", "services: {}\n")
			writeReplaceFile(t, src, "README.md", "hi\n")
		})
		if joined := strings.Join(lines, "\n"); strings.Contains(joined, reported) {
			t.Errorf("identical files wrongly reported as replaced; log:\n%s", joined)
		}
	})
}
