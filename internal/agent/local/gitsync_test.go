package local

import (
	"context"
	"net/http"
	"net/http/cgi" //nolint:gosec // G504: serves git-http-backend; the Httpoxy bug was fixed in Go 1.6.3
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/windlass-dev/windlass/internal/agent"
)

// TestGitSyncTokenStaysOutOfArgv clones and fetches a private repository
// from a real git http-backend that demands the token, through a git shim
// that records every command line git is started with.
func TestGitSyncTokenStaysOutOfArgv(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	execPath, err := exec.Command(gitBin, "--exec-path").Output() //nolint:gosec // G204: the test drives git directly
	if err != nil {
		t.Skip("git --exec-path failed")
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("git-http-backend not installed")
	}

	// A bare repository with one commit on main.
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
	git(work, "commit", "-q", "-m", "init")
	git(root, "clone", "-q", "--bare", work, filepath.Join(root, "repos", "app.git"))

	const token = "ghs_secretTOKEN123"
	want := "Basic " + basicAuth("x-access-token", token)
	cgiHandler := &cgi.Handler{
		Path: backend,
		Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Join(root, "repos"), "GIT_HTTP_EXPORT_ALL=1"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "auth required", http.StatusUnauthorized)
			return
		}
		cgiHandler.ServeHTTP(w, r)
	}))
	defer srv.Close()

	// The shim logs argv, then runs the real git.
	shimDir := t.TempDir()
	argvLog := filepath.Join(shimDir, "argv.log")
	shim := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + argvLog + "\nexec " + gitBin + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil { //nolint:gosec // G306: a test shim must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	l, err := New(Config{ProjectsDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	req := agent.GitSyncReq{Project: "app", URL: srv.URL + "/app.git", Branch: "main", Token: token}
	ctx := context.Background()
	first, err := hostLocal{l}.GitSync(ctx, req, nil)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	// The second sync takes the fetch path.
	second, err := hostLocal{l}.GitSync(ctx, req, nil)
	if err != nil || second.Commit != first.Commit || len(first.Commit) != 40 {
		t.Fatalf("fetch = %+v, %v; clone gave %q", second, err, first.Commit)
	}

	logged, err := os.ReadFile(argvLog) //nolint:gosec // G304: a file under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "clone") || !strings.Contains(string(logged), "fetch") {
		t.Fatalf("shim did not see clone and fetch:\n%s", logged)
	}
	if strings.Contains(string(logged), basicAuth("x-access-token", token)) || strings.Contains(string(logged), "extraheader") {
		t.Fatalf("token on a git command line:\n%s", logged)
	}
	// "--" ends the options, so a URL starting with "-" cannot become one.
	if !strings.Contains(string(logged), "--single-branch -- "+req.URL+" ") {
		t.Fatalf("clone URL not after --:\n%s", logged)
	}

	// Without the token the server refuses, so the clone above really used it.
	req.Project, req.Token = "anon", ""
	if _, err := (hostLocal{l}).GitSync(ctx, req, nil); err == nil {
		t.Fatal("clone without a token succeeded; the server is not checking")
	}
}

func TestGitConfigEnvAppends(t *testing.T) {
	env := gitConfigEnv([]string{"A=1", "GIT_CONFIG_PARAMETERS='core.x=1'"}, "http.extraheader", "it's")
	if env[1] != `GIT_CONFIG_PARAMETERS='core.x=1' 'http.extraheader=it'\''s'` || len(env) != 2 {
		t.Fatalf("env = %q", env)
	}
}

// TestGitSyncAfterRestore syncs a project whose src came back from a backup.
// Backups skip .git and a restore replaces the whole directory, so src holds
// the files but no repository, and a plain clone refuses the non-empty path.
func TestGitSyncAfterRestore(t *testing.T) {
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
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
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
	ctx := context.Background()
	req := agent.GitSyncReq{Project: "app", URL: bare, Branch: "main"}
	if _, err := (hostLocal{l}).GitSync(ctx, req, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// What a restore leaves: the files, an edit, and no .git.
	src := filepath.Join(projects, "app", "src")
	if err := os.RemoveAll(filepath.Join(src, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "compose.yaml"), []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "compose.yaml"), []byte("services: {web: {image: nginx}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(work, "commit", "-q", "-am", "two")
	git(work, "push", "-q", bare, "main")
	head := git(work, "rev-parse", "HEAD")

	// A sync that fails partway must leave no half-made repository behind,
	// or every later sync takes it for a checkout and fetches the bad remote.
	bad := req
	bad.URL = filepath.Join(root, "missing.git")
	if _, err := (hostLocal{l}).GitSync(ctx, bad, nil); err == nil {
		t.Fatal("sync from a missing remote succeeded")
	}

	got, err := (hostLocal{l}).GitSync(ctx, req, nil)
	if err != nil {
		t.Fatalf("sync after restore: %v", err)
	}
	if got.Commit != head {
		t.Fatalf("commit = %s, want %s", got.Commit, head)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "compose.yaml")); string(b) != "services: {web: {image: nginx}}\n" { //nolint:gosec // G304: a file under t.TempDir()
		t.Fatalf("compose.yaml = %q, want the remote's", b)
	}
	// And the repaired checkout keeps working on the ordinary fetch path.
	if again, err := (hostLocal{l}).GitSync(ctx, req, nil); err != nil || again.Commit != head {
		t.Fatalf("next sync = %+v, %v", again, err)
	}
}
