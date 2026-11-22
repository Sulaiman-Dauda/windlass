//go:build linux

package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

// TestGitSyncCheckoutIgnoresServiceUmask: the windlass unit runs with
// UMask=0077, and a checkout written under it is owner-only, which a Docker
// build copies into the image.
func TestGitSyncCheckoutIgnoresServiceUmask(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	work := filepath.Join(root, "work")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command(gitBin, args...) //nolint:gosec // G204: the test drives git directly
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(work, "docker"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "docker", "entrypoint.sh"), []byte("#!/bin/sh\n"), 0o700); err != nil { //nolint:gosec // G306: an executable fixture
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "app.js"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	git("add", ".")
	git("commit", "-q", "-m", "one")

	projects := t.TempDir()
	l, err := New(Config{ProjectsDir: projects})
	if err != nil {
		t.Fatal(err)
	}
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	if _, err := (hostLocal{l}).GitSync(context.Background(), agent.GitSyncReq{Project: "app", URL: work, Branch: "main"}, nil); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(projects, "app", "src")
	for path, want := range map[string]os.FileMode{
		"docker/entrypoint.sh": 0o755, "app.js": 0o644, "docker": 0o755,
	} {
		info, err := os.Stat(filepath.Join(src, path))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
}
