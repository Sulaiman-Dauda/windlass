package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newFS(t *testing.T) (fsLocal, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := New(Config{ProjectsDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return fsLocal{l}, dir
}

func TestFSWriteReadList(t *testing.T) {
	f, root := newFS(t)
	ctx := context.Background()

	if _, err := f.EnsureProject(ctx, "crm"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if err := f.WriteFile(ctx, "crm", "compose.yaml", []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Content is on the real filesystem where a user could edit it by hand.
	onDisk, err := os.ReadFile(filepath.Join(root, "crm", "compose.yaml"))
	if err != nil || string(onDisk) != "services: {}\n" {
		t.Fatalf("on-disk = %q, %v", onDisk, err)
	}

	got, err := f.ReadFile(ctx, "crm", "compose.yaml")
	if err != nil || string(got) != "services: {}\n" {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}

	list, err := f.List(ctx, "crm", ".")
	if err != nil || len(list) != 1 || list[0].Name != "compose.yaml" {
		t.Fatalf("List = %+v, %v", list, err)
	}
}

func TestFSPathTraversalBlocked(t *testing.T) {
	f, root := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "crm"); err != nil {
		t.Fatal(err)
	}

	// Plant a sentinel outside the project to prove it is unreachable.
	sentinel := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(sentinel, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	bad := []string{
		"../outside.txt",
		"..\\outside.txt",
		"a/../../outside.txt",
		"/etc/passwd",
		`C:\Windows\win.ini`,
		"..",
	}
	for _, rel := range bad {
		if _, err := f.ReadFile(ctx, "crm", rel); err == nil {
			t.Errorf("ReadFile(%q) succeeded, traversal not blocked", rel)
		}
		if err := f.WriteFile(ctx, "crm", rel, []byte("x"), 0o644); err == nil {
			t.Errorf("WriteFile(%q) succeeded, traversal not blocked", rel)
		}
	}

	// Bad project names are rejected outright.
	for _, proj := range []string{"../crm", "crm/../x", "CRM", "", "a b", ".hidden"} {
		if _, err := f.EnsureProject(ctx, proj); err == nil {
			t.Errorf("EnsureProject(%q) succeeded, want error", proj)
		}
	}
}

func TestFSSymlinkTraversalBlocked(t *testing.T) {
	f, root := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "crm"); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	sentinel := filepath.Join(outside, "secret.json")
	if err := os.WriteFile(sentinel, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "crm")
	if err := os.Symlink(sentinel, filepath.Join(project, "leak.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}

	if _, err := f.ReadFile(ctx, "crm", "leak.json"); err == nil {
		t.Error("ReadFile followed a symlink outside the project")
	}
	if _, err := f.List(ctx, "crm", "escape"); err == nil {
		t.Error("List followed a symlink outside the project")
	}
	if err := f.WriteFile(ctx, "crm", "escape/config.json", []byte("escaped"), 0o600); err == nil {
		t.Error("WriteFile followed a parent symlink outside the project")
	}
	if _, err := os.Stat(filepath.Join(outside, "config.json")); !os.IsNotExist(err) {
		t.Errorf("outside file was created: %v", err)
	}

	if err := os.Symlink(outside, filepath.Join(root, "linked-project")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.EnsureProject(ctx, "linked-project"); err == nil {
		t.Error("EnsureProject accepted a symlinked project directory")
	}
}

func TestFSAtomicWriteReplaces(t *testing.T) {
	f, _ := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "crm"); err != nil {
		t.Fatal(err)
	}

	if err := f.WriteFile(ctx, "crm", ".env", []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(ctx, "crm", ".env", []byte("A=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := f.ReadFile(ctx, "crm", ".env")
	if err != nil || string(got) != "A=2\n" {
		t.Fatalf("after replace: %q, %v", got, err)
	}

	// No temp files left behind.
	list, _ := f.List(ctx, "crm", ".")
	for _, fi := range list {
		if fi.Name != ".env" {
			t.Errorf("leftover file %q", fi.Name)
		}
	}
}

func TestRemoveProject(t *testing.T) {
	f, root := newFS(t)
	ctx := context.Background()
	if _, err := f.EnsureProject(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(ctx, "gone", "compose.yaml", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveProject(ctx, "gone"); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "gone")); !os.IsNotExist(err) {
		t.Error("project directory still exists")
	}
}

func TestCopyProjectStaysInsideBothProjects(t *testing.T) {
	f, root := newFS(t)
	ctx := context.Background()
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("host only"), 0o600); err != nil {
		t.Fatal(err)
	}
	live, staging := filepath.Join(root, "app"), filepath.Join(root, "app-staging")
	for _, d := range []string{filepath.Join(live, "src", "public"), filepath.Join(live, "data"), staging} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(live, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, rel string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(live, rel)); err != nil {
			t.Fatal(err)
		}
	}
	write("compose.yaml", "services: {}\n")
	write("src/public/index.html", "hi")
	write("data/big.db", "skip me")
	link("index.html", "src/public/home.html")
	link(secret, "src/absolute")
	link("../../../outside", "src/relative")
	link(filepath.Dir(secret), "src/dir")
	// Reads as inside, but here resolves to src, so the .. leaves the
	// project and lands in its sibling.
	link(".", "here")
	link("here/../app/compose.yaml", "sibling")
	link("here/../not-yet/file", "sibling-missing")
	link("missing.txt", "src/dangling")

	skipped, err := f.CopyProject(ctx, "app", "app-staging", []string{"data"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(staging, "src", "public", "index.html")); string(got) != "hi" { //nolint:gosec // G304: the test's temp dir
		t.Errorf("index.html = %q", got)
	}
	if got, err := os.Readlink(filepath.Join(staging, "src", "public", "home.html")); err != nil || got != "index.html" {
		t.Errorf("inside link = %q, %v", got, err)
	}
	if got, err := os.Readlink(filepath.Join(staging, "src", "dangling")); err != nil || got != "missing.txt" {
		t.Errorf("dangling inside link = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(staging, "data")); !os.IsNotExist(err) {
		t.Errorf("skipped data was copied: %v", err)
	}
	want := map[string]bool{"src/absolute": true, "src/relative": true, "src/dir": true, "sibling": true, "sibling-missing": true}
	for _, s := range skipped {
		if !want[s] {
			t.Errorf("unexpected skip %q", s)
		}
		delete(want, s)
	}
	if len(want) != 0 {
		t.Errorf("links not skipped: %v (skipped %v)", want, skipped)
	}

	// A file the walk saw as regular and a container then swapped for a
	// link out of the project: the open refuses rather than reading it.
	in, _ := os.OpenRoot(live)
	defer in.Close()
	out, _ := os.OpenRoot(staging)
	defer out.Close()
	if err := copyFile(in, out, "src/absolute", 0o644); err == nil {
		t.Fatal("copyFile followed a link out of the project")
	}
	if _, err := os.Stat(filepath.Join(staging, "src", "absolute")); !os.IsNotExist(err) {
		t.Errorf("staging got a file from outside: %v", err)
	}
}
